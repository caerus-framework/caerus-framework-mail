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
