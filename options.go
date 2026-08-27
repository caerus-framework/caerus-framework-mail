package cf_mail

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	cf_configuration "github.com/caerus-framework/caerus-framework-configuration"
)

// Option configures the mail component at construction time.
type Option func(*options)

type options struct {
	loaded       *MailConfig
	configSource string
	configPath   string
	srcEnvPrefix string
	srcFormat    cf_configuration.Format
	srcFormatSet bool
	provider     string
	fromAddress  string
	timeout      time.Duration
	httpClient   *http.Client
	resend       ResendSettings
	ses          SESSettings
	unisender    UnisenderGoSettings
	logger       *slog.Logger
	loggerSet    bool
	name         string
}

// SourceOption configures the self-registered configuration source created by
// WithConfigSource.
type SourceOption func(*sourceOptions)

type sourceOptions struct {
	envPrefix string
	format    cf_configuration.Format
	formatSet bool
}

// WithSourceEnvPrefix sets the environment overlay prefix for the source
// (default: the uppercase source name with "-" replaced by "_", plus "_").
// An empty prefix disables env overlay.
func WithSourceEnvPrefix(prefix string) SourceOption {
	return func(o *sourceOptions) { o.envPrefix = prefix }
}

// WithSourceFormat forces the file format instead of inferring it from the
// path extension (".yaml"/".yml" → YAML; anything else JSON).
func WithSourceFormat(f cf_configuration.Format) SourceOption {
	return func(o *sourceOptions) { o.format = f; o.formatSet = true }
}

func defaultSourceEnvPrefix(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_"
}

// WithConfig sets a static configuration snapshot. Non-zero fields of cfg
// override the values set by the convenience options. Prefer WithConfigSource
// when using caerus-framework-configuration with hot-reload.
func WithConfig(cfg MailConfig) Option {
	return func(o *options) { o.loaded = &cfg }
}

// WithConfigSource binds this component to a named configuration source and
// registers that source with the configuration component (via the framework's
// ConfigSourceRegistrar pass during argv absorption).
//
//	cf_mail.New(cf_mail.WithConfigSource("mail", "config/mail.json"))
//
// A path of "" registers an env-only (fileless) source when the EnvPrefix is
// non-empty. The path CLI override stays --<source-name> (ParseFlags).
// Declares a dependency on "configuration".
func WithConfigSource(name, path string, opts ...SourceOption) Option {
	return func(o *options) {
		so := sourceOptions{envPrefix: defaultSourceEnvPrefix(name)}
		for _, opt := range opts {
			opt(&so)
		}
		o.configSource = name
		o.configPath = path
		o.srcEnvPrefix = so.envPrefix
		o.srcFormat = so.format
		o.srcFormatSet = so.formatSet
	}
}

// WithProvider selects the sender (resend, ses, unisender_go). Required unless
// the bound config file sets provider.
func WithProvider(provider string) Option {
	return func(o *options) { o.provider = provider }
}

// WithFromAddress sets the soft-default sender. Send uses it when Mail.From
// is empty. A non-empty Mail.From overrides this call only.
func WithFromAddress(from string) Option {
	return func(o *options) { o.fromAddress = from }
}

// WithTimeout sets the per-send HTTP timeout (default 10s).
func WithTimeout(d time.Duration) Option {
	return func(o *options) { o.timeout = d }
}

// WithHTTPClient overrides the HTTP client used by every provider. Useful for
// tests (a stub RoundTripper). The component does not close a client it did
// not create.
func WithHTTPClient(hc *http.Client) Option {
	return func(o *options) { o.httpClient = hc }
}

// WithResendAPIKey sets the Resend API key (tests, embedded use).
func WithResendAPIKey(apiKey string) Option {
	return func(o *options) { o.resend.APIKey = apiKey }
}

// WithResendBaseURL overrides the Resend API endpoint.
func WithResendBaseURL(baseURL string) Option {
	return func(o *options) { o.resend.BaseURL = baseURL }
}

// WithResendProfiles sets named Resend senders (api_key + from per name).
// Prefer the config file's resend.profiles in production.
func WithResendProfiles(profiles map[string]ResendProfile) Option {
	return func(o *options) { o.resend.Profiles = cloneResendProfiles(profiles) }
}

// WithResendDefaultProfile names the profiles entry Send uses by default.
func WithResendDefaultProfile(name string) Option {
	return func(o *options) { o.resend.DefaultProfile = name }
}

// WithSESRegion sets the AWS region for SES v2.
func WithSESRegion(region string) Option {
	return func(o *options) { o.ses.Region = region }
}

// WithSESCredentials sets static AWS keys. Leave unset to use the default
// credential chain (IRSA / shared config).
func WithSESCredentials(accessKeyID, secretAccessKey string) Option {
	return func(o *options) {
		o.ses.AccessKeyID = accessKeyID
		o.ses.SecretAccessKey = secretAccessKey
	}
}

// WithSESEndpoint overrides the SES v2 endpoint (LocalStack / tests).
func WithSESEndpoint(endpoint string) Option {
	return func(o *options) { o.ses.Endpoint = endpoint }
}

// WithUnisenderGoAPIKey sets the Unisender Go API key.
func WithUnisenderGoAPIKey(apiKey string) Option {
	return func(o *options) { o.unisender.APIKey = apiKey }
}

// WithUnisenderGoBaseURL overrides the Unisender Go transactional base URL.
func WithUnisenderGoBaseURL(baseURL string) Option {
	return func(o *options) { o.unisender.BaseURL = baseURL }
}

// WithLogger overrides the logger used for component diagnostics. By default
// the component logs through the framework logs component.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) { o.logger = logger; o.loggerSet = true }
}

// WithName sets a custom component name, allowing multiple mail instances in
// the same process. The default name is "mail" (ComponentName).
func WithName(name string) Option {
	return func(o *options) { o.name = name }
}
