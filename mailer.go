package cf_mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	cf "github.com/caerus-framework/caerus-framework"
	cf_configuration "github.com/caerus-framework/caerus-framework-configuration"
	cf_logs "github.com/caerus-framework/caerus-framework-logs"
	cf_observability "github.com/caerus-framework/caerus-framework-observability"
	"github.com/resend/resend-go/v2"
)

const (
	// ComponentName is the framework component name for the mail component.
	ComponentName = "mail"

	// ComponentStage is the stage data-layer components initialize in.
	ComponentStage = cf.Stage("data")
)

// CFMail is the caerus-framework-mail component. Apps hold this pointer and
// call Send per use. The active provider is a config setting, not a second
// component type.
type CFMail struct {
	mu           sync.RWMutex
	configSource string
	configPath   string
	srcEnvPrefix string
	srcFormat    cf_configuration.Format
	srcFormatSet bool
	provider     string
	from         string
	timeout      time.Duration
	httpClient   *http.Client
	resend       ResendSettings
	ses          SESSettings
	unisender    UnisenderGoSettings
	loggerSet    bool
	sender       mailSender
	logger       *slog.Logger
	logsSub      *cf_logs.Subscription
	fw           *cf.CaerusFramework
	name         string
	meter        *sendMeter
	reloads      atomic.Uint64
}

type mailSender interface {
	Send(ctx context.Context, from string, m Mail) (id string, err error)
	Provider() string
}

// New creates a mail component. The provider client is built at Init, not here.
func New(opts ...Option) *CFMail {
	o := options{
		logger:  slog.Default(),
		timeout: 10 * time.Second,
	}
	for _, opt := range opts {
		opt(&o)
	}
	c := &CFMail{
		configSource: o.configSource,
		configPath:   o.configPath,
		srcEnvPrefix: o.srcEnvPrefix,
		srcFormat:    o.srcFormat,
		srcFormatSet: o.srcFormatSet,
		provider:     o.provider,
		from:         o.fromAddress,
		timeout:      o.timeout,
		httpClient:   o.httpClient,
		resend:       o.resend,
		ses:          o.ses,
		unisender:    o.unisender,
		logger:       o.logger,
		loggerSet:    o.loggerSet,
		name:         o.name,
		meter:        newSendMeter(),
	}
	if o.loaded != nil {
		c.applyConfig(*o.loaded)
	}
	return c
}

func (c *CFMail) applyConfig(cfg MailConfig) {
	cfg = cfg.mergeFlatEnv()
	if cfg.Provider != "" {
		c.provider = cfg.Provider
	}
	if cfg.FromAddress != "" {
		c.from = cfg.FromAddress
	}
	if cfg.TimeoutSec > 0 {
		c.timeout = time.Duration(cfg.TimeoutSec * float64(time.Second))
	}
	if cfg.Resend.APIKey != "" {
		c.resend.APIKey = cfg.Resend.APIKey
	}
	if cfg.Resend.BaseURL != "" {
		c.resend.BaseURL = cfg.Resend.BaseURL
	}
	if cfg.Resend.DefaultProfile != "" {
		c.resend.DefaultProfile = cfg.Resend.DefaultProfile
	}
	if cfg.Resend.Profiles != nil {
		c.resend.Profiles = cloneResendProfiles(cfg.Resend.Profiles)
	}
	if cfg.SES.Region != "" {
		c.ses.Region = cfg.SES.Region
	}
	if cfg.SES.AccessKeyID != "" {
		c.ses.AccessKeyID = cfg.SES.AccessKeyID
	}
	if cfg.SES.SecretAccessKey != "" {
		c.ses.SecretAccessKey = cfg.SES.SecretAccessKey
	}
	if cfg.SES.Endpoint != "" {
		c.ses.Endpoint = cfg.SES.Endpoint
	}
	if cfg.UnisenderGo.APIKey != "" {
		c.unisender.APIKey = cfg.UnisenderGo.APIKey
	}
	if cfg.UnisenderGo.BaseURL != "" {
		c.unisender.BaseURL = cfg.UnisenderGo.BaseURL
	}
	if cfg.UnisenderGo.SkipUnsubscribe != nil {
		c.unisender.SkipUnsubscribe = cfg.UnisenderGo.SkipUnsubscribe
	}
}

// Name implements cf.CaerusComponent.
func (c *CFMail) Name() string {
	if c.name != "" {
		return c.name
	}
	return ComponentName
}

// GetInitOrderStage implements cf.CaerusComponent.
func (c *CFMail) GetInitOrderStage() cf.Stage { return ComponentStage }

// GetDependencies implements cf.Dependencies.
func (c *CFMail) GetDependencies() []string {
	deps := []string{cf_logs.ComponentName}
	if c.configSource != "" {
		deps = append(deps, cf_configuration.ComponentName)
	}
	return deps
}

// Init implements cf.CaerusComponent.
func (c *CFMail) Init(ctx context.Context, fw *cf.CaerusFramework) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sender != nil {
		return nil
	}
	c.fw = fw
	if !c.loggerSet {
		if logs, ok := cf.Get[*cf_logs.Logs](fw); ok {
			c.logsSub = logs.OnReconfigureFor(c.Name(), func(l *slog.Logger) { c.logger = l })
		}
	}
	if c.configSource != "" {
		if err := c.applyConfigFromSource(); err != nil {
			return err
		}
	} else if c.from == "" {
		c.logger.Warn("cf_mail: no from_address configured; Send must set Mail.From")
	}
	sender, err := c.buildSender(ctx)
	if err != nil {
		return err
	}
	c.sender = sender
	c.logger.Info("cf_mail: initialized",
		"provider", sender.Provider(),
		"from", c.from,
		cf_logs.SecretSet("credential", c.credentialForLog()),
	)
	return nil
}

func (c *CFMail) applyConfigFromSource() error {
	conf, ok := cf.Get[*cf_configuration.Configuration](c.fw)
	if !ok {
		return errors.New("cf_mail: configuration component not registered")
	}
	loaded, ok := cf_configuration.Get[MailConfig](conf, c.configSource)
	if !ok {
		return fmt.Errorf("cf_mail: configuration source %q not found", c.configSource)
	}
	c.applyConfig(loaded)
	// File is canonical: replace Resend profile map (including clear) so a
	// removed profile does not stick after reload.
	merged := loaded.mergeFlatEnv()
	c.resend.Profiles = cloneResendProfiles(merged.Resend.Profiles)
	c.resend.DefaultProfile = merged.Resend.DefaultProfile
	return nil
}

func (c *CFMail) buildSender(ctx context.Context) (mailSender, error) {
	provider, err := normalizeProvider(c.provider)
	if err != nil {
		return nil, err
	}
	c.provider = provider
	switch provider {
	case ProviderResend:
		return c.newResendSender()
	case ProviderSES:
		return c.newSESSender(ctx)
	case ProviderUnisenderGo:
		return c.newUnisenderGoSender()
	default:
		return nil, fmt.Errorf("cf_mail: unknown provider %q", provider)
	}
}

func (c *CFMail) credentialForLog() string {
	switch c.provider {
	case ProviderResend:
		if c.resend.APIKey != "" {
			return c.resend.APIKey
		}
		if dp := strings.TrimSpace(c.resend.DefaultProfile); dp != "" {
			if p, ok := c.resend.Profiles[dp]; ok {
				return p.APIKey
			}
		}
		if len(c.resend.Profiles) > 0 {
			return "profiles"
		}
		return ""
	case ProviderSES:
		if c.ses.AccessKeyID != "" {
			return c.ses.SecretAccessKey
		}
		return "default-chain"
	case ProviderUnisenderGo:
		return c.unisender.APIKey
	default:
		return ""
	}
}

// OnConfigReload implements cf.ConfigReloader. On failure the previous sender
// is kept (last-good).
func (c *CFMail) OnConfigReload(source string, cfg any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if source != c.configSource || c.sender == nil || c.fw == nil {
		return
	}
	if _, ok := cfg.(*MailConfig); !ok {
		c.logger.Error("cf_mail: config reload rejected", "source", source, "type", fmt.Sprintf("%T", cfg))
		return
	}
	prevProvider, prevFrom, prevTimeout := c.provider, c.from, c.timeout
	prevResend, prevSES, prevUni := c.resend, c.ses, c.unisender
	prevResend.Profiles = cloneResendProfiles(c.resend.Profiles)
	if err := c.applyConfigFromSource(); err != nil {
		c.logger.Error("cf_mail: config reload rejected; keeping previous", "err", err)
		return
	}
	newSender, err := c.buildSender(context.Background())
	if err != nil {
		c.provider, c.from, c.timeout = prevProvider, prevFrom, prevTimeout
		c.resend, c.ses, c.unisender = prevResend, prevSES, prevUni
		c.logger.Error("cf_mail: config reload create sender failed; keeping previous", "err", err)
		return
	}
	c.sender = newSender
	c.reloads.Add(1)
	c.logger.Info("cf_mail: reconfigured after config reload",
		"provider", newSender.Provider(),
		"from", c.from,
		cf_logs.SecretSet("credential", c.credentialForLog()),
	)
}

// RegisterConfigSources implements cf.ConfigSourceRegistrar.
func (c *CFMail) RegisterConfigSources(conf any) error {
	cfg, ok := conf.(*cf_configuration.Configuration)
	if !ok {
		return fmt.Errorf("cf_mail: RegisterConfigSources: expected configuration component, got %T", conf)
	}
	if c.configSource == "" {
		return nil
	}
	format := c.srcFormat
	if !c.srcFormatSet {
		if p := strings.ToLower(c.configPath); strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml") {
			format = cf_configuration.FormatYAML
		} else {
			format = cf_configuration.FormatJSON
		}
	}
	return cf_configuration.AddSource(cfg, cf_configuration.Source[MailConfig]{
		Name:      c.configSource,
		Path:      c.configPath,
		Format:    format,
		Owner:     c.Name(),
		EnvPrefix: c.srcEnvPrefix,
		Validate:  validateMailConfig,
	})
}

// Shutdown implements cf.CaerusComponent.
func (c *CFMail) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.logsSub != nil {
		c.logsSub.Unsubscribe()
		c.logsSub = nil
	}
	c.sender = nil
	return nil
}

// Provider returns the normalized provider name after Init (empty before).
func (c *CFMail) Provider() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.sender != nil {
		return c.sender.Provider()
	}
	return c.provider
}

// From returns the configured soft-default sender. When Resend
// default_profile is active, that is the profile's from_address.
func (c *CFMail) From() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if m, ok := c.sender.(*resendMux); ok && m.defProfile != "" {
		if p, ok := m.profiles[m.defProfile]; ok {
			return p.from
		}
	}
	return c.from
}

// ResendClient returns the live Resend SDK client for the default sender, or
// nil when the active provider is not resend (or before Init / after Shutdown).
// With named profiles this is the legacy api_key client, or the
// default_profile client when that setting is set — not a profile picked via
// SendWithProfile.
func (c *CFMail) ResendClient() *resend.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	switch s := c.sender.(type) {
	case *resendSender:
		return s.client
	case *resendMux:
		def, _, err := s.defaultSender()
		if err != nil || def == nil {
			return nil
		}
		return def.client
	default:
		return nil
	}
}

// ResendProfiles returns the sorted names of configured Resend profiles
// (empty when the provider is not resend or no profiles are set).
func (c *CFMail) ResendProfiles() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if m, ok := c.sender.(*resendMux); ok {
		out := make([]string, len(m.profileKeys))
		copy(out, m.profileKeys)
		return out
	}
	return nil
}

// SESClient returns the live SES v2 client, or nil when the active provider
// is not ses (or before Init / after Shutdown).
func (c *CFMail) SESClient() *sesv2.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if s, ok := c.sender.(*sesSender); ok {
		return s.client
	}
	return nil
}

func (c *CFMail) liveSender() mailSender {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sender
}

// Send sends m through the configured provider and returns the provider
// message id (Resend id, SES MessageId, Unisender Go job_id).
//
// From: from_address / WithFromAddress is a soft default when Mail.From is
// empty. When Resend default_profile is set, that profile's from_address is
// the soft default instead. If both are empty, or the resolved From or any To
// address does not parse (`net/mail.ParseAddress`), Send fails. HTML and Text
// may both be set; at least one must be non-empty.
//
// For a named Resend API key / From pair, use SendWithProfile.
//
// If the first HTTP status is 429 or 5xx, Send waits (Retry-After, capped at
// 1s) and tries once more while ctx is live. 4xx other than 429 and network
// errors are not retried.
func (c *CFMail) Send(ctx context.Context, m Mail) (string, error) {
	sender := c.liveSender()
	if sender == nil {
		return "", errors.New("cf_mail: Send before Init or after Shutdown")
	}
	defaultFrom := c.From()
	if mux, ok := sender.(*resendMux); ok {
		if _, profileFrom, err := mux.defaultSender(); err != nil {
			return "", err
		} else if mux.defProfile != "" {
			defaultFrom = profileFrom
		}
	}
	return c.sendMail(ctx, sender, defaultFrom, m)
}

// SendWithProfile sends m with a named Resend profile (resend.profiles[name]).
// Soft-default From is that profile's from_address; Mail.From still overrides.
// SES and Unisender Go have no profiles — use Send and set Mail.From instead.
func (c *CFMail) SendWithProfile(ctx context.Context, profile string, m Mail) (string, error) {
	sender := c.liveSender()
	if sender == nil {
		return "", errors.New("cf_mail: SendWithProfile before Init or after Shutdown")
	}
	mux, ok := sender.(*resendMux)
	if !ok {
		return "", errors.New("cf_mail: SendWithProfile requires resend.profiles")
	}
	p, err := mux.profile(profile)
	if err != nil {
		return "", err
	}
	return c.sendMail(ctx, p.sender, p.from, m)
}

func (c *CFMail) sendMail(ctx context.Context, sender mailSender, defaultFrom string, m Mail) (string, error) {
	from, err := resolveFrom(m.From, defaultFrom)
	if err != nil {
		return "", err
	}
	to, err := validateTo(m.To)
	if err != nil {
		return "", err
	}
	m.To = to
	if reply := strings.TrimSpace(m.ReplyTo); reply != "" {
		if err := validateMailbox("ReplyTo", reply); err != nil {
			return "", err
		}
		m.ReplyTo = reply
	}
	if m.HTML == "" && m.Text == "" {
		return "", errors.New("cf_mail: Mail needs HTML or Text")
	}
	id, err, slot := c.sendOnce(ctx, sender, from, m)
	if err == nil {
		return id, nil
	}
	if !shouldRetryStatus(slot.code) {
		return "", err
	}
	if waitErr := waitRetry(ctx, retryWaitDuration(slot.retryAfter)); waitErr != nil {
		return "", err
	}
	c.meter.addRetry(from)
	id, err, _ = c.sendOnce(ctx, sender, from, m)
	return id, err
}

func (c *CFMail) sendOnce(ctx context.Context, sender mailSender, from string, m Mail) (string, error, httpStatusSlot) {
	slot := &httpStatusSlot{}
	sendCtx := context.WithValue(ctx, fromCtxKey{}, from)
	sendCtx = context.WithValue(sendCtx, httpStatusKey{}, slot)
	id, err := sender.Send(sendCtx, from, m)
	if err != nil {
		return "", &SendError{err: err, status: slot.code}, *slot
	}
	return id, nil, *slot
}

// Health implements cf.HealthProvider. Providers expose no liveness endpoint;
// health reflects that a sender is initialized.
func (c *CFMail) Health(ctx context.Context) error {
	if c.liveSender() == nil {
		return errors.New("cf_mail: sender is not initialized")
	}
	return nil
}

// Metrics implements cf_observability.MetricsProvider.
func (c *CFMail) Metrics() []cf_observability.Metric {
	if c.liveSender() == nil {
		return nil
	}
	infoLabels := map[string]string{
		"component": c.Name(),
		"provider":  c.Provider(),
		"from":      c.From(),
	}
	ms := []cf_observability.Metric{
		{
			Name:   "mail_info",
			Help:   "Mail client descriptor; 1 while initialized.",
			Value:  1,
			Labels: copyLabels(infoLabels),
		},
		{
			Name:   "mail_config_reloads_total",
			Help:   "Total number of successful sender rebuilds after a configuration reload.",
			Value:  float64(c.reloads.Load()),
			Labels: map[string]string{"component": c.Name()},
			Type:   cf_observability.MetricTypeCounter,
		},
	}
	senders := c.meter.snapshot()
	if len(senders) == 0 {
		defaultFrom := c.From()
		if defaultFrom == "" {
			defaultFrom = unknownFrom
		}
		return c.trafficMetrics(ms, defaultFrom, senderStats{})
	}
	froms := make([]string, 0, len(senders))
	for from := range senders {
		froms = append(froms, from)
	}
	sort.Strings(froms)
	for _, from := range froms {
		ms = c.trafficMetrics(ms, from, senders[from])
	}
	return ms
}

func (c *CFMail) trafficMetrics(ms []cf_observability.Metric, from string, s senderStats) []cf_observability.Metric {
	labels := map[string]string{"component": c.Name(), "from": from}
	ms = append(ms,
		cf_observability.Metric{
			Name:   "mail_emails_sent_total",
			Help:   "Total number of emails accepted by the provider (HTTP 2xx).",
			Value:  float64(s.sent),
			Labels: copyLabels(labels),
			Type:   cf_observability.MetricTypeCounter,
		},
		cf_observability.Metric{
			Name:   "mail_send_duration_seconds_sum",
			Help:   "Total send latency in seconds (sum of all attempts).",
			Value:  s.durationSum,
			Labels: copyLabels(labels),
			Type:   cf_observability.MetricTypeCounter,
		},
		cf_observability.Metric{
			Name:   "mail_send_duration_seconds_count",
			Help:   "Total number of send attempts (2xx and failures).",
			Value:  float64(s.durationCount),
			Labels: copyLabels(labels),
			Type:   cf_observability.MetricTypeCounter,
		},
		cf_observability.Metric{
			Name:   "mail_send_retries_total",
			Help:   "Total number of second Send attempts after HTTP 429 or 5xx.",
			Value:  float64(s.retries),
			Labels: copyLabels(labels),
			Type:   cf_observability.MetricTypeCounter,
		},
	)
	codes := make([]string, 0, len(s.failed))
	for code := range s.failed {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		fl := copyLabels(labels)
		fl["error_code"] = code
		ms = append(ms, cf_observability.Metric{
			Name:   "mail_emails_failed_total",
			Help:   "Total number of failed email sends, keyed by error code (HTTP status, or \"network\" for transport errors).",
			Value:  float64(s.failed[code]),
			Labels: fl,
			Type:   cf_observability.MetricTypeCounter,
		})
	}
	return ms
}

func copyLabels(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

var _ cf.CaerusComponent = (*CFMail)(nil)
var _ cf.Dependencies = (*CFMail)(nil)
var _ cf.HealthProvider = (*CFMail)(nil)
var _ cf_observability.MetricsProvider = (*CFMail)(nil)
var _ cf.ConfigReloader = (*CFMail)(nil)
var _ cf.ConfigSourceRegistrar = (*CFMail)(nil)
