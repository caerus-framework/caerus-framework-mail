package cf_mail

import (
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
)

// Mail is the Caerus send DTO (SES-simple shape). Apps pass this to Send
// without importing a provider SDK. Attachments, Cc/Bcc, headers, and
// scheduled send stay on the provider escape hatches (ResendClient / SESClient)
// for callers that opt into an SDK.
type Mail struct {
	// From overrides from_address when non-empty. Empty (or whitespace)
	// uses the configured soft default. The resolved value must parse as
	// an RFC 5322 address (`net/mail.ParseAddress`).
	From    string
	To      []string
	Subject string
	HTML    string
	Text    string
	ReplyTo string
	// Tags are provider metadata (name → value). Empty names are skipped.
	// Resend and SES send name/value pairs. Unisender Go uses the names as
	// its string tags (max 4) and the pairs as global_metadata (max 10).
	Tags map[string]string
	// IdempotencyKey is forwarded when the provider supports it (Resend
	// Idempotency-Key header; Unisender Go idempotence_key). SES v2 SendEmail
	// has no matching field; it is ignored there.
	IdempotencyKey string
}

func resolveFrom(mailFrom, defaultFrom string) (string, error) {
	from := strings.TrimSpace(mailFrom)
	if from == "" {
		from = strings.TrimSpace(defaultFrom)
	}
	if from == "" {
		return "", errors.New("cf_mail: no From (set from_address / WithFromAddress, or Mail.From)")
	}
	if err := validateMailbox("From", from); err != nil {
		return "", err
	}
	return from, nil
}

func validateTo(to []string) ([]string, error) {
	if len(to) == 0 {
		return nil, errors.New("cf_mail: Mail.To is empty")
	}
	out := make([]string, 0, len(to))
	for i, raw := range to {
		addr := strings.TrimSpace(raw)
		if addr == "" {
			return nil, fmt.Errorf("cf_mail: Mail.To[%d] is empty", i)
		}
		if err := validateMailbox("To", addr); err != nil {
			return nil, err
		}
		out = append(out, addr)
	}
	return out, nil
}

func validateMailbox(field, addr string) error {
	if _, err := mail.ParseAddress(addr); err != nil {
		return fmt.Errorf("cf_mail: invalid %s %q: %w", field, addr, err)
	}
	return nil
}

func parseMailbox(addr string) (name, email string, err error) {
	a, err := mail.ParseAddress(addr)
	if err != nil {
		return "", "", err
	}
	return a.Name, a.Address, nil
}

func sortedTagNames(tags map[string]string) []string {
	if len(tags) == 0 {
		return nil
	}
	names := make([]string, 0, len(tags))
	for name := range tags {
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
