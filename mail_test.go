package cf_mail

import "testing"

func TestResolveFrom(t *testing.T) {
	got, err := resolveFrom("", "noreply@x.io")
	if err != nil || got != "noreply@x.io" {
		t.Fatalf("default = %q %v", got, err)
	}
	got, err = resolveFrom("  Brand <b@x.io>  ", "noreply@x.io")
	if err != nil || got != "Brand <b@x.io>" {
		t.Fatalf("override = %q %v", got, err)
	}
	if _, err := resolveFrom("", ""); err == nil {
		t.Fatal("both empty must fail")
	}
	if _, err := resolveFrom("not-an-email", "noreply@x.io"); err == nil {
		t.Fatal("invalid override must fail (not fall back)")
	}
}

func TestValidateTo(t *testing.T) {
	if _, err := validateTo(nil); err == nil {
		t.Fatal("empty To must fail")
	}
	got, err := validateTo([]string{"  a@x.io  "})
	if err != nil || len(got) != 1 || got[0] != "a@x.io" {
		t.Fatalf("trim To = %q %v", got, err)
	}
}

func TestParseMailbox(t *testing.T) {
	name, email, err := parseMailbox("Brand <b@x.io>")
	if err != nil || name != "Brand" || email != "b@x.io" {
		t.Fatalf("got %q %q %v", name, email, err)
	}
}

func TestNormalizeProvider(t *testing.T) {
	got, err := normalizeProvider("Unisender-Go")
	if err != nil || got != ProviderUnisenderGo {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := normalizeProvider("unisender"); err == nil {
		t.Fatal("campaign Unisender must be rejected")
	}
	if _, err := normalizeProvider(""); err == nil {
		t.Fatal("empty provider must fail")
	}
	if _, err := normalizeProvider("smtp"); err == nil {
		t.Fatal("unknown provider must fail")
	}
}

func TestMergeFlatEnv(t *testing.T) {
	cfg := MailConfig{
		ResendAPIKey: "from-env",
		Resend:       ResendSettings{APIKey: "from-file"},
	}.mergeFlatEnv()
	if cfg.Resend.APIKey != "from-file" {
		t.Fatalf("nested file should win when set, got %q", cfg.Resend.APIKey)
	}
	cfg = MailConfig{ResendAPIKey: "from-env"}.mergeFlatEnv()
	if cfg.Resend.APIKey != "from-env" {
		t.Fatalf("flat env fills empty nested, got %q", cfg.Resend.APIKey)
	}
}
