package cf_mail

import "testing"

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
