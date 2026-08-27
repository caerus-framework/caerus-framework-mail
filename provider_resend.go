package cf_mail

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/resend/resend-go/v2"
)

type resendSender struct {
	client *resend.Client
}

func (s *resendSender) Provider() string { return ProviderResend }

func (s *resendSender) Send(ctx context.Context, from string, m Mail) (string, error) {
	req := &resend.SendEmailRequest{
		From:    from,
		To:      append([]string(nil), m.To...),
		Subject: m.Subject,
		Html:    m.HTML,
		Text:    m.Text,
		ReplyTo: m.ReplyTo,
	}
	if names := sortedTagNames(m.Tags); len(names) > 0 {
		req.Tags = make([]resend.Tag, 0, len(names))
		for _, name := range names {
			req.Tags = append(req.Tags, resend.Tag{Name: name, Value: m.Tags[name]})
		}
	}
	var (
		resp *resend.SendEmailResponse
		err  error
	)
	if m.IdempotencyKey != "" {
		resp, err = s.client.Emails.SendWithOptions(ctx, req, &resend.SendEmailOptions{IdempotencyKey: m.IdempotencyKey})
	} else {
		resp, err = s.client.Emails.SendWithContext(ctx, req)
	}
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", errors.New("cf_mail: resend: empty send response")
	}
	return resp.Id, nil
}

// resendMux is the Resend backend when named profiles exist (and optionally a
// legacy default key). Send uses the default entry; SendWithProfile picks by name.
type resendMux struct {
	def         *resendSender
	defFrom     string // soft default for Send when no default_profile
	defProfile  string // name of profiles entry used by Send; empty → def
	profiles    map[string]resendProfileRuntime
	profileKeys []string // sorted names for Metrics / Profiles()
}

type resendProfileRuntime struct {
	from   string
	sender *resendSender
}

func (m *resendMux) Provider() string { return ProviderResend }

func (m *resendMux) Send(ctx context.Context, from string, mail Mail) (string, error) {
	s, _, err := m.defaultSender()
	if err != nil {
		return "", err
	}
	return s.Send(ctx, from, mail)
}

func (m *resendMux) defaultSender() (*resendSender, string, error) {
	if m.defProfile != "" {
		p, ok := m.profiles[m.defProfile]
		if !ok || p.sender == nil {
			return nil, "", fmt.Errorf("cf_mail: resend: default_profile %q is not built", m.defProfile)
		}
		return p.sender, p.from, nil
	}
	if m.def == nil {
		return nil, "", errors.New("cf_mail: resend: no default sender (set resend.api_key or resend.default_profile, or use SendWithProfile)")
	}
	return m.def, m.defFrom, nil
}

func (m *resendMux) profile(name string) (resendProfileRuntime, error) {
	n := strings.TrimSpace(name)
	if n == "" {
		return resendProfileRuntime{}, errors.New("cf_mail: resend: profile name is empty")
	}
	p, ok := m.profiles[n]
	if !ok || p.sender == nil {
		return resendProfileRuntime{}, fmt.Errorf("cf_mail: resend: unknown profile %q", n)
	}
	return p, nil
}

func (c *CFMail) newResendSender() (mailSender, error) {
	if err := validateResendProfiles(c.resend); err != nil {
		return nil, err
	}
	sharedBase := strings.TrimSpace(c.resend.BaseURL)
	profiles := make(map[string]resendProfileRuntime, len(c.resend.Profiles))
	names := make([]string, 0, len(c.resend.Profiles))
	for name, p := range c.resend.Profiles {
		base := strings.TrimSpace(p.BaseURL)
		if base == "" {
			base = sharedBase
		}
		s, err := c.buildResendClient(strings.TrimSpace(p.APIKey), base)
		if err != nil {
			return nil, fmt.Errorf("cf_mail: resend.profiles[%q]: %w", name, err)
		}
		profiles[name] = resendProfileRuntime{
			from:   strings.TrimSpace(p.FromAddress),
			sender: s,
		}
		names = append(names, name)
	}
	sort.Strings(names)

	var def *resendSender
	if key := strings.TrimSpace(c.resend.APIKey); key != "" {
		s, err := c.buildResendClient(key, sharedBase)
		if err != nil {
			return nil, err
		}
		def = s
	}
	defProfile := strings.TrimSpace(c.resend.DefaultProfile)
	if defProfile != "" {
		if _, ok := profiles[defProfile]; !ok {
			return nil, fmt.Errorf("cf_mail: resend: default_profile %q is not in profiles", defProfile)
		}
	}
	if def == nil && defProfile == "" && len(profiles) == 0 {
		return nil, errors.New("cf_mail: resend: no API key configured (resend.api_key / MAIL_RESEND_API_KEY / WithResendAPIKey, or resend.profiles)")
	}

	if len(profiles) == 0 && defProfile == "" {
		return def, nil
	}
	return &resendMux{
		def:         def,
		defFrom:     strings.TrimSpace(c.from),
		defProfile:  defProfile,
		profiles:    profiles,
		profileKeys: names,
	}, nil
}

func (c *CFMail) buildResendClient(apiKey, baseURL string) (*resendSender, error) {
	if apiKey == "" {
		return nil, errors.New("no API key configured")
	}
	hc := c.meteredClient()
	client := resend.NewCustomClient(hc, apiKey)
	if baseURL != "" {
		parsed, err := url.Parse(baseURL)
		if err != nil {
			return nil, fmt.Errorf("invalid base_url %q: %w", baseURL, err)
		}
		client.BaseURL = parsed
	}
	return &resendSender{client: client}, nil
}
