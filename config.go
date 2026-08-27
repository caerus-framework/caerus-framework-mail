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
//
// Two shapes are supported (they may be combined):
//
//  1. Legacy single key — api_key + top-level from_address. Send uses that
//     pair. Flat MAIL_RESEND_API_KEY still fills api_key.
//  2. Named profiles — profiles map (each api_key + from_address). Use
//     SendWithProfile to pick one. Optional default_profile makes Send use
//     that profile when set.
//
// Profiles exist because Resend API keys are often bound to one domain.
type ResendSettings struct {
	APIKey  string `json:"api_key" yaml:"api_key" env:"-" secret:"redact"`
	BaseURL string `json:"base_url,omitempty" yaml:"base_url,omitempty" env:"-"`
	// DefaultProfile names the profiles entry Send uses when set. Empty
	// keeps Send on the legacy api_key + top-level from_address.
	DefaultProfile string `json:"default_profile,omitempty" yaml:"default_profile,omitempty" env:"-"`
	// Profiles are named Resend senders (one API key + From per name).
	Profiles map[string]ResendProfile `json:"profiles,omitempty" yaml:"profiles,omitempty" env:"-"`
}

// ResendProfile is one named Resend API key + From pair.
type ResendProfile struct {
	APIKey      string `json:"api_key" yaml:"api_key" secret:"redact"`
	FromAddress string `json:"from_address" yaml:"from_address"`
	// BaseURL overrides ResendSettings.BaseURL for this profile only.
	BaseURL string `json:"base_url,omitempty" yaml:"base_url,omitempty"`
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
// Soft-default From must be resolvable at load time, except Resend Path A
// profiles-only configs that only call SendWithProfile:
//
//   - top-level from_address, or
//   - resend.default_profile → that profile's from_address, or
//   - non-empty resend.profiles (each profile already has from_address)
func validateMailConfig(cfg *MailConfig) error {
	merged := cfg.mergeFlatEnv()
	if err := validateResendProfiles(merged.Resend); err != nil {
		return err
	}
	if strings.TrimSpace(merged.FromAddress) != "" {
		return nil
	}
	if dp := strings.TrimSpace(merged.Resend.DefaultProfile); dp != "" {
		if p, ok := merged.Resend.Profiles[dp]; ok && strings.TrimSpace(p.FromAddress) != "" {
			return nil
		}
	}
	if len(merged.Resend.Profiles) > 0 {
		return nil
	}
	return errors.New("cf_mail: from_address is required (or set resend.default_profile / resend.profiles)")
}

func validateResendProfiles(r ResendSettings) error {
	if len(r.Profiles) == 0 {
		if strings.TrimSpace(r.DefaultProfile) != "" {
			return errors.New("cf_mail: resend.default_profile set but resend.profiles is empty")
		}
		return nil
	}
	for name, p := range r.Profiles {
		n := strings.TrimSpace(name)
		if n == "" {
			return errors.New("cf_mail: resend.profiles: empty profile name")
		}
		if n != name {
			return fmt.Errorf("cf_mail: resend.profiles: profile name %q has leading/trailing space", name)
		}
		if strings.TrimSpace(p.APIKey) == "" {
			return fmt.Errorf("cf_mail: resend.profiles[%q]: api_key is required", name)
		}
		if strings.TrimSpace(p.FromAddress) == "" {
			return fmt.Errorf("cf_mail: resend.profiles[%q]: from_address is required", name)
		}
	}
	if dp := strings.TrimSpace(r.DefaultProfile); dp != "" {
		if _, ok := r.Profiles[dp]; !ok {
			return fmt.Errorf("cf_mail: resend.default_profile %q is not in resend.profiles", dp)
		}
	}
	return nil
}

func cloneResendProfiles(in map[string]ResendProfile) map[string]ResendProfile {
	if in == nil {
		return nil
	}
	out := make(map[string]ResendProfile, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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
