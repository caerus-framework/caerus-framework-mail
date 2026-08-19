package cf_mail

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/resend/resend-go/v2"
)

type resendSender struct {
	client *resend.Client
}

func (s *resendSender) Provider() string { return ProviderResend }

func (c *CFMail) newResendSender() (*resendSender, error) {
	if c.resend.APIKey == "" {
		return nil, errors.New("cf_mail: resend: no API key configured (resend.api_key / MAIL_RESEND_API_KEY / WithResendAPIKey)")
	}
	hc := c.meteredClient()
	client := resend.NewCustomClient(hc, c.resend.APIKey)
	if c.resend.BaseURL != "" {
		parsed, err := url.Parse(c.resend.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("cf_mail: resend: invalid base_url %q: %w", c.resend.BaseURL, err)
		}
		client.BaseURL = parsed
	}
	return &resendSender{client: client}, nil
}

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
