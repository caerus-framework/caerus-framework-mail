package cf_mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultUnisenderGoBaseURL = "https://goapi.unisender.ru/en/transactional/api/v1"

type unisenderGoSender struct {
	http      *http.Client
	apiKey    string
	baseURL   string
	skipUnsub int
}

func (s *unisenderGoSender) Provider() string { return ProviderUnisenderGo }

func (c *CFMail) newUnisenderGoSender() (*unisenderGoSender, error) {
	if c.unisender.APIKey == "" {
		return nil, errors.New("cf_mail: unisender_go: no API key configured (unisender_go.api_key / MAIL_UNISENDER_GO_API_KEY / WithUnisenderGoAPIKey)")
	}
	base := strings.TrimSpace(c.unisender.BaseURL)
	if base == "" {
		base = defaultUnisenderGoBaseURL
	}
	base = strings.TrimRight(base, "/")
	skip := 1 // transactional default: do not append a campaign unsubscribe block
	if c.unisender.SkipUnsubscribe != nil && !*c.unisender.SkipUnsubscribe {
		skip = 0
	}
	return &unisenderGoSender{
		http:      c.meteredClient(),
		apiKey:    c.unisender.APIKey,
		baseURL:   base,
		skipUnsub: skip,
	}, nil
}

type unisenderSendRequest struct {
	Message unisenderMessage `json:"message"`
}

type unisenderMessage struct {
	Recipients      []unisenderRecipient `json:"recipients"`
	Subject         string               `json:"subject"`
	FromEmail       string               `json:"from_email"`
	FromName        string               `json:"from_name,omitempty"`
	ReplyTo         string               `json:"reply_to,omitempty"`
	ReplyToName     string               `json:"reply_to_name,omitempty"`
	Body            unisenderBody        `json:"body"`
	Tags            []string             `json:"tags,omitempty"`
	GlobalMetadata  map[string]string    `json:"global_metadata,omitempty"`
	SkipUnsubscribe int                  `json:"skip_unsubscribe"`
	IdempotenceKey  string               `json:"idempotence_key,omitempty"`
}

type unisenderRecipient struct {
	Email string `json:"email"`
}

type unisenderBody struct {
	HTML      string `json:"html,omitempty"`
	Plaintext string `json:"plaintext,omitempty"`
}

type unisenderSendResponse struct {
	Status       string            `json:"status"`
	JobID        string            `json:"job_id"`
	FailedEmails map[string]string `json:"failed_emails"`
	Code         int               `json:"code"`
	Message      string            `json:"message"`
}

func (s *unisenderGoSender) Send(ctx context.Context, from string, m Mail) (string, error) {
	fromName, fromEmail, err := parseMailbox(from)
	if err != nil {
		return "", err
	}
	recipients := make([]unisenderRecipient, 0, len(m.To))
	for _, to := range m.To {
		_, email, err := parseMailbox(to)
		if err != nil {
			return "", err
		}
		recipients = append(recipients, unisenderRecipient{Email: email})
	}
	msg := unisenderMessage{
		Recipients:      recipients,
		Subject:         m.Subject,
		FromEmail:       fromEmail,
		FromName:        fromName,
		Body:            unisenderBody{HTML: m.HTML, Plaintext: m.Text},
		SkipUnsubscribe: s.skipUnsub,
		IdempotenceKey:  m.IdempotencyKey,
	}
	if m.ReplyTo != "" {
		replyName, replyEmail, err := parseMailbox(m.ReplyTo)
		if err != nil {
			return "", err
		}
		msg.ReplyTo = replyEmail
		msg.ReplyToName = replyName
	}
	if names := sortedTagNames(m.Tags); len(names) > 0 {
		maxTags := 4
		if len(names) < maxTags {
			maxTags = len(names)
		}
		msg.Tags = names[:maxTags]
		meta := make(map[string]string, len(names))
		for i, name := range names {
			if i >= 10 {
				break
			}
			meta[name] = m.Tags[name]
		}
		msg.GlobalMetadata = meta
	}
	body, err := json.Marshal(unisenderSendRequest{Message: msg})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/email/send.json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-KEY", s.apiKey)
	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("cf_mail: unisender_go: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out unisenderSendResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("cf_mail: unisender_go: decode response: %w", err)
	}
	if out.Status != "" && out.Status != "success" {
		if out.Message != "" {
			return "", fmt.Errorf("cf_mail: unisender_go: %s (code %d)", out.Message, out.Code)
		}
		return "", fmt.Errorf("cf_mail: unisender_go: status %q", out.Status)
	}
	if len(out.FailedEmails) > 0 && len(out.FailedEmails) >= len(m.To) {
		return "", fmt.Errorf("cf_mail: unisender_go: all recipients failed: %v", out.FailedEmails)
	}
	if out.JobID == "" {
		return "", errors.New("cf_mail: unisender_go: empty job_id")
	}
	return out.JobID, nil
}
