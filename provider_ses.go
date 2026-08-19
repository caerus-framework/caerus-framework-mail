package cf_mail

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

type sesSender struct {
	client *sesv2.Client
}

func (s *sesSender) Provider() string { return ProviderSES }

func (c *CFMail) newSESSender(ctx context.Context) (*sesSender, error) {
	if c.ses.Region == "" {
		return nil, errors.New("cf_mail: ses: region is required (ses.region / MAIL_SES_REGION / WithSESRegion)")
	}
	id, secret := c.ses.AccessKeyID, c.ses.SecretAccessKey
	if (id == "") != (secret == "") {
		return nil, errors.New("cf_mail: ses: access_key_id and secret_access_key must both be set, or both empty (default AWS chain)")
	}
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(c.ses.Region),
		config.WithHTTPClient(c.meteredClient()),
		config.WithRetryMaxAttempts(1),
	}
	if id != "" {
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(id, secret, "")))
	}
	if c.ses.Endpoint != "" {
		opts = append(opts, config.WithBaseEndpoint(c.ses.Endpoint))
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("cf_mail: ses: load aws config: %w", err)
	}
	return &sesSender{client: sesv2.NewFromConfig(awsCfg)}, nil
}

func (s *sesSender) Send(ctx context.Context, from string, m Mail) (string, error) {
	in := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(from),
		Destination: &types.Destination{
			ToAddresses: append([]string(nil), m.To...),
		},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{Data: aws.String(m.Subject), Charset: aws.String("UTF-8")},
				Body:    &types.Body{},
			},
		},
	}
	if m.HTML != "" {
		in.Content.Simple.Body.Html = &types.Content{Data: aws.String(m.HTML), Charset: aws.String("UTF-8")}
	}
	if m.Text != "" {
		in.Content.Simple.Body.Text = &types.Content{Data: aws.String(m.Text), Charset: aws.String("UTF-8")}
	}
	if m.ReplyTo != "" {
		in.ReplyToAddresses = []string{m.ReplyTo}
	}
	if names := sortedTagNames(m.Tags); len(names) > 0 {
		in.EmailTags = make([]types.MessageTag, 0, len(names))
		for _, name := range names {
			in.EmailTags = append(in.EmailTags, types.MessageTag{
				Name:  aws.String(name),
				Value: aws.String(m.Tags[name]),
			})
		}
	}
	out, err := s.client.SendEmail(ctx, in)
	if err != nil {
		return "", err
	}
	if out == nil || out.MessageId == nil || *out.MessageId == "" {
		return "", errors.New("cf_mail: ses: empty MessageId")
	}
	return *out.MessageId, nil
}
