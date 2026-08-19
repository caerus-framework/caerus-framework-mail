package cf_mail

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// ProviderResend is the Resend transactional API (resend.com).
	ProviderResend = "resend"
	// ProviderSES is Amazon SES v2 SendEmail (transactional).
	ProviderSES = "ses"
	// ProviderUnisenderGo is Unisender Go transactional email/send
	// (goapi.unisender.ru). It is not the Unisender.com campaign API.
	ProviderUnisenderGo = "unisender_go"
)

// MailConfig is the file/env-drivable configuration. Shared settings sit at
// the top; each provider has a nested object. The configuration overlay does
// not walk nested structs for env, so the flat MAIL_* aliases below exist for
// local/`go run` (same pattern as valkey-state RATE_LIMIT_*).
type MailConfig struct {
	// Provider selects the sender: resend, ses, or unisender_go.
	Provider string `json:"provider" yaml:"provider" env:"PROVIDER"`
	// FromAddress is the soft-default sender. Send uses it when Mail.From is empty.
	FromAddress string `json:"from_address" yaml:"from_address" env:"FROM_ADDRESS"`
	// TimeoutSec bounds each provider HTTP call (default 10s).
	TimeoutSec float64 `json:"timeout_sec,omitempty" yaml:"timeout_sec,omitempty" env:"TIMEOUT_SEC"`

	Resend      ResendSettings      `json:"resend,omitempty" yaml:"resend,omitempty" env:"-"`
	SES         SESSettings         `json:"ses,omitempty" yaml:"ses,omitempty" env:"-"`
	UnisenderGo UnisenderGoSettings `json:"unisender_go,omitempty" yaml:"unisender_go,omitempty" env:"-"`

	// Flat env aliases (nested JSON/YAML still wins when both are set after
	// merge: env overlay fills these, applyConfig copies them into the nested
	// structs when the nested field is still empty).
	ResendAPIKey         string `json:"-" yaml:"-" env:"RESEND_API_KEY" secret:"redact"`
	ResendBaseURL        string `json:"-" yaml:"-" env:"RESEND_BASE_URL"`
	SESRegion            string `json:"-" yaml:"-" env:"SES_REGION"`
	SESAccessKeyID       string `json:"-" yaml:"-" env:"SES_ACCESS_KEY_ID"`
	SESSecretAccessKey   string `json:"-" yaml:"-" env:"SES_SECRET_ACCESS_KEY" secret:"redact"`
	SESEndpoint          string `json:"-" yaml:"-" env:"SES_ENDPOINT"`
	UnisenderGoAPIKey    string `json:"-" yaml:"-" env:"UNISENDER_GO_API_KEY" secret:"redact"`
	UnisenderGoBaseURL   string `json:"-" yaml:"-" env:"UNISENDER_GO_BASE_URL"`
	UnisenderGoSkipUnsub *bool  `json:"-" yaml:"-" env:"UNISENDER_GO_SKIP_UNSUBSCRIBE"`
}

// ResendSettings is the nested Resend blob.
type ResendSettings struct {
	APIKey  string `json:"api_key" yaml:"api_key" env:"-" secret:"redact"`
	BaseURL string `json:"base_url,omitempty" yaml:"base_url,omitempty" env:"-"`
}

// SESSettings is the nested Amazon SES v2 blob.
//
// Credentials: set both AccessKeyID and SecretAccessKey in the file (K8s
// Secret mount). Leave both empty to use the default AWS chain (IRSA in
// cluster, shared config on a laptop). Setting only one is an Init error.
type SESSettings struct {
	Region          string `json:"region" yaml:"region" env:"-"`
	AccessKeyID     string `json:"access_key_id,omitempty" yaml:"access_key_id,omitempty" env:"-"`
	SecretAccessKey string `json:"secret_access_key,omitempty" yaml:"secret_access_key,omitempty" env:"-" secret:"redact"`
	Endpoint        string `json:"endpoint,omitempty" yaml:"endpoint,omitempty" env:"-"`
}

// UnisenderGoSettings is the nested Unisender Go transactional blob.
// Default BaseURL is https://goapi.unisender.ru/en/transactional/api/v1
// (override for go1/go2 datacenters). This is not api.unisender.com.
type UnisenderGoSettings struct {
	APIKey          string `json:"api_key" yaml:"api_key" env:"-" secret:"redact"`
	BaseURL         string `json:"base_url,omitempty" yaml:"base_url,omitempty" env:"-"`
	SkipUnsubscribe *bool  `json:"skip_unsubscribe,omitempty" yaml:"skip_unsubscribe,omitempty" env:"-"`
}

func (cfg MailConfig) mergeFlatEnv() MailConfig {
	if cfg.Resend.APIKey == "" {
		cfg.Resend.APIKey = cfg.ResendAPIKey
	}
	if cfg.Resend.BaseURL == "" {
		cfg.Resend.BaseURL = cfg.ResendBaseURL
	}
	if cfg.SES.Region == "" {
		cfg.SES.Region = cfg.SESRegion
	}
	if cfg.SES.AccessKeyID == "" {
		cfg.SES.AccessKeyID = cfg.SESAccessKeyID
	}
	if cfg.SES.SecretAccessKey == "" {
		cfg.SES.SecretAccessKey = cfg.SESSecretAccessKey
	}
	if cfg.SES.Endpoint == "" {
		cfg.SES.Endpoint = cfg.SESEndpoint
	}
	if cfg.UnisenderGo.APIKey == "" {
		cfg.UnisenderGo.APIKey = cfg.UnisenderGoAPIKey
	}
	if cfg.UnisenderGo.BaseURL == "" {
		cfg.UnisenderGo.BaseURL = cfg.UnisenderGoBaseURL
	}
	if cfg.UnisenderGo.SkipUnsubscribe == nil {
		cfg.UnisenderGo.SkipUnsubscribe = cfg.UnisenderGoSkipUnsub
	}
	return cfg
}

// validateMailConfig runs on every successful load of a registered mail source.
// from_address must be non-empty so misconfiguration fails at startup or reload
// rather than on the first Send.
func validateMailConfig(cfg *MailConfig) error {
	if strings.TrimSpace(cfg.mergeFlatEnv().FromAddress) == "" {
		return errors.New("cf_mail: from_address is required")
	}
	return nil
}

func normalizeProvider(raw string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(raw))
	p = strings.ReplaceAll(p, "-", "_")
	switch p {
	case ProviderResend, ProviderSES, ProviderUnisenderGo:
		return p, nil
	case "unisendergo":
		return ProviderUnisenderGo, nil
	case "unisender":
		return "", fmt.Errorf("cf_mail: provider %q is the Unisender campaign API; use %q (Unisender Go transactional)", raw, ProviderUnisenderGo)
	case "":
		return "", fmt.Errorf("cf_mail: provider is required (%s, %s, or %s)", ProviderResend, ProviderSES, ProviderUnisenderGo)
	default:
		return "", fmt.Errorf("cf_mail: unknown provider %q (want %s, %s, or %s)", raw, ProviderResend, ProviderSES, ProviderUnisenderGo)
	}
}
