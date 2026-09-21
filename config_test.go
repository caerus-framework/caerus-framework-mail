package cf_mail

import (
	"strings"
	"testing"
)

func TestValidateMailConfigRequiresFromAddress(t *testing.T) {
	if err := validateMailConfig(&MailConfig{
		Provider: ProviderResend,
		Resend:   ResendSettings{APIKey: "re_k"},
	}); err == nil {
		t.Fatal("expected error for missing from_address")
	}
	if err := validateMailConfig(&MailConfig{
		Provider:    ProviderResend,
		FromAddress: "   ",
		Resend:      ResendSettings{APIKey: "re_k"},
	}); err == nil {
		t.Fatal("expected error for whitespace-only from_address")
	}
	if err := validateMailConfig(&MailConfig{
		Provider:    ProviderResend,
		FromAddress: "noreply@example.com",
		Resend:      ResendSettings{APIKey: "re_k"},
	}); err != nil {
		t.Fatalf("valid config: %v", err)
	}
}

func TestValidateMailConfigResendProfiles(t *testing.T) {
	if err := validateMailConfig(&MailConfig{
		Provider: ProviderResend,
		Resend: ResendSettings{
			Profiles: map[string]ResendProfile{
				"kronos": {APIKey: "re_k", FromAddress: "noreply@kronos.example"},
			},
		},
	}); err != nil {
		t.Fatalf("profiles-only should allow empty top from_address: %v", err)
	}
	if err := validateMailConfig(&MailConfig{
		Provider: ProviderResend,
		Resend: ResendSettings{
			DefaultProfile: "kronos",
			Profiles: map[string]ResendProfile{
				"kronos": {APIKey: "re_k", FromAddress: "noreply@kronos.example"},
			},
		},
	}); err != nil {
		t.Fatalf("default_profile with from should validate: %v", err)
	}
	if err := validateMailConfig(&MailConfig{
		Provider: ProviderResend,
		Resend: ResendSettings{
			DefaultProfile: "missing",
			Profiles: map[string]ResendProfile{
				"kronos": {APIKey: "re_k", FromAddress: "noreply@kronos.example"},
			},
		},
	}); err == nil {
		t.Fatal("expected error for unknown default_profile")
	}
	if err := validateMailConfig(&MailConfig{
		Provider: ProviderResend,
		Resend: ResendSettings{
			Profiles: map[string]ResendProfile{
				"kronos": {APIKey: "", FromAddress: "noreply@kronos.example"},
			},
		},
	}); err == nil {
		t.Fatal("expected error for profile missing api_key")
	}
	if err := validateMailConfig(&MailConfig{
		Provider: ProviderResend,
		Resend: ResendSettings{
			DefaultProfile: "kronos",
		},
	}); err == nil {
		t.Fatal("expected error for default_profile without profiles")
	}
}

func TestValidateHTTPSEndpoints(t *testing.T) {
	ok := MailConfig{
		Provider:    ProviderResend,
		FromAddress: "noreply@example.com",
		Resend:      ResendSettings{APIKey: "re_k", BaseURL: "https://api.resend.com"},
	}
	if err := validateMailConfig(&ok); err != nil {
		t.Fatalf("https base_url: %v", err)
	}
	httpURL := MailConfig{
		Provider:    ProviderResend,
		FromAddress: "noreply@example.com",
		Resend:      ResendSettings{APIKey: "re_k", BaseURL: "http://127.0.0.1:9"},
	}
	if err := validateMailConfig(&httpURL); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("http base_url: %v", err)
	}
	sesHTTP := MailConfig{
		Provider:    ProviderSES,
		FromAddress: "noreply@example.com",
		SES:         SESSettings{Region: "eu-central-1", Endpoint: "http://localhost:4566"},
	}
	if err := validateMailConfig(&sesHTTP); err == nil || !strings.Contains(err.Error(), "ses.endpoint") {
		t.Fatalf("http ses.endpoint: %v", err)
	}
	uniHTTP := MailConfig{
		Provider:    ProviderUnisenderGo,
		FromAddress: "noreply@example.com",
		UnisenderGo: UnisenderGoSettings{APIKey: "ug", BaseURL: "http://goapi.example/v1"},
	}
	if err := validateMailConfig(&uniHTTP); err == nil || !strings.Contains(err.Error(), "unisender_go.base_url") {
		t.Fatalf("http unisender base_url: %v", err)
	}
	profileHTTP := MailConfig{
		Provider: ProviderResend,
		Resend: ResendSettings{
			Profiles: map[string]ResendProfile{
				"kronos": {APIKey: "re_k", FromAddress: "a@x.io", BaseURL: "http://evil.invalid"},
			},
		},
	}
	if err := validateMailConfig(&profileHTTP); err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("http profile base_url: %v", err)
	}
	emptyOK := MailConfig{
		Provider:    ProviderResend,
		FromAddress: "noreply@example.com",
		Resend:      ResendSettings{APIKey: "re_k"},
	}
	if err := validateMailConfig(&emptyOK); err != nil {
		t.Fatalf("empty base_url (SDK default https): %v", err)
	}
}
