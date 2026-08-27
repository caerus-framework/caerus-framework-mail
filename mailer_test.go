package cf_mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cf "github.com/caerus-framework/caerus-framework"
	cf_configuration "github.com/caerus-framework/caerus-framework-configuration"
	cf_logs "github.com/caerus-framework/caerus-framework-logs"
	cf_observability "github.com/caerus-framework/caerus-framework-observability"
)

func testMail(to string) Mail {
	return Mail{To: []string{to}, Subject: "t", Text: "t"}
}

func addComponent(t *testing.T, fw *cf.CaerusFramework, c cf.CaerusComponent) {
	t.Helper()
	if err := fw.AddComponent(c); err != nil {
		t.Fatalf("AddComponent: %v", err)
	}
}

func writeConfig(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

type stubRoundTripper struct {
	lastReq  *http.Request
	lastBody string
	calls    int
	body     string
	code     int
	header   http.Header
	err      error
	bodies   []string
	codes    []int
}

func (s *stubRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	s.calls++
	s.lastReq = r
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		s.lastBody = string(b)
		r.Body = io.NopCloser(bytes.NewReader(b))
	}
	if s.err != nil {
		return nil, s.err
	}
	i := s.calls - 1
	code, body := s.code, s.body
	if i < len(s.codes) {
		code = s.codes[i]
	}
	if i < len(s.bodies) {
		body = s.bodies[i]
	}
	hdr := http.Header{"Content-Type": []string{"application/json"}}
	if s.header != nil {
		hdr = s.header.Clone()
	}
	return &http.Response{
		StatusCode: code,
		Header:     hdr,
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestComponentContract(t *testing.T) {
	m := New()
	if m.Name() != ComponentName {
		t.Fatalf("Name() = %q, want %q", m.Name(), ComponentName)
	}
	if m.GetInitOrderStage() != ComponentStage {
		t.Fatalf("GetInitOrderStage() = %q", m.GetInitOrderStage())
	}
	if m.ResendClient() != nil || m.SESClient() != nil {
		t.Fatal("SDK clients should be nil before Init")
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown before Init: %v", err)
	}
}

func TestHealthAndMetricsBeforeInit(t *testing.T) {
	m := New()
	if err := m.Health(context.Background()); err == nil {
		t.Fatal("Health before Init should fail")
	}
	if ms := m.Metrics(); ms != nil {
		t.Fatalf("Metrics before Init = %+v, want nil", ms)
	}
	var _ cf.HealthProvider = m
	var _ cf_observability.MetricsProvider = m
}

func TestInitRequiresProvider(t *testing.T) {
	m := New(WithResendAPIKey("re_key"), WithFromAddress("a@x.io"))
	if err := m.Init(context.Background(), cf.New()); err == nil {
		t.Fatal("Init without provider should fail")
	}
}

func TestInitRejectsCampaignUnisender(t *testing.T) {
	m := New(WithProvider("unisender"), WithUnisenderGoAPIKey("k"), WithFromAddress("a@x.io"))
	err := m.Init(context.Background(), cf.New())
	if err == nil || !strings.Contains(err.Error(), "unisender_go") {
		t.Fatalf("want campaign-API error, got %v", err)
	}
}

func TestInitResendBuildsClient(t *testing.T) {
	m := New(WithProvider(ProviderResend), WithResendAPIKey("re_key"), WithFromAddress("noreply@x.io"))
	if err := m.Init(context.Background(), cf.New()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	if m.ResendClient() == nil {
		t.Fatal("ResendClient() should be non-nil")
	}
	if m.Provider() != ProviderResend {
		t.Fatalf("Provider() = %q", m.Provider())
	}
}

func TestSendResend(t *testing.T) {
	stub := &stubRoundTripper{body: `{"id":"email_1"}`, code: 200}
	m := New(WithProvider(ProviderResend), WithResendAPIKey("re_key"), WithFromAddress("noreply@x.io"),
		WithHTTPClient(&http.Client{Transport: stub}))
	if err := m.Init(context.Background(), cf.New()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	id, err := m.Send(context.Background(), Mail{To: []string{"user@example.com"}, Subject: "hi", HTML: "<p>hi</p>"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "email_1" {
		t.Fatalf("id = %q", id)
	}
	if !strings.HasPrefix(stub.lastReq.Header.Get("Authorization"), "Bearer re_key") {
		t.Fatalf("Authorization = %q", stub.lastReq.Header.Get("Authorization"))
	}
}

func TestSendWithProfileResend(t *testing.T) {
	stub := &stubRoundTripper{body: `{"id":"email_p"}`, code: 200}
	m := New(
		WithProvider(ProviderResend),
		WithResendProfiles(map[string]ResendProfile{
			"kronos":       {APIKey: "re_kronos", FromAddress: "noreply@kronos.example"},
			"stock-market": {APIKey: "re_sm", FromAddress: "hello@stock.example"},
		}),
		WithHTTPClient(&http.Client{Transport: stub}),
	)
	if err := m.Init(context.Background(), cf.New()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	if got := m.ResendProfiles(); len(got) != 2 || got[0] != "kronos" || got[1] != "stock-market" {
		t.Fatalf("ResendProfiles() = %v", got)
	}
	if _, err := m.Send(context.Background(), testMail("user@example.com")); err == nil {
		t.Fatal("Send without default should fail when only profiles are set")
	}
	if _, err := m.SendWithProfile(context.Background(), "missing", testMail("user@example.com")); err == nil {
		t.Fatal("unknown profile should fail")
	}

	id, err := m.SendWithProfile(context.Background(), "kronos", testMail("user@example.com"))
	if err != nil {
		t.Fatalf("SendWithProfile: %v", err)
	}
	if id != "email_p" {
		t.Fatalf("id = %q", id)
	}
	if !strings.HasPrefix(stub.lastReq.Header.Get("Authorization"), "Bearer re_kronos") {
		t.Fatalf("Authorization = %q", stub.lastReq.Header.Get("Authorization"))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stub.lastBody), &payload); err != nil {
		t.Fatalf("body: %v", err)
	}
	if payload["from"] != "noreply@kronos.example" {
		t.Fatalf("from = %v", payload["from"])
	}

	stub.calls = 0
	id, err = m.SendWithProfile(context.Background(), "stock-market", testMail("user@example.com"))
	if err != nil {
		t.Fatalf("SendWithProfile stock-market: %v", err)
	}
	if id != "email_p" {
		t.Fatalf("id = %q", id)
	}
	if !strings.HasPrefix(stub.lastReq.Header.Get("Authorization"), "Bearer re_sm") {
		t.Fatalf("Authorization = %q", stub.lastReq.Header.Get("Authorization"))
	}
}

func TestSendResendDefaultProfile(t *testing.T) {
	stub := &stubRoundTripper{body: `{"id":"email_d"}`, code: 200}
	m := New(
		WithProvider(ProviderResend),
		WithResendDefaultProfile("kronos"),
		WithResendProfiles(map[string]ResendProfile{
			"kronos": {APIKey: "re_kronos", FromAddress: "noreply@kronos.example"},
		}),
		WithHTTPClient(&http.Client{Transport: stub}),
	)
	if err := m.Init(context.Background(), cf.New()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	if m.From() != "noreply@kronos.example" {
		t.Fatalf("From() = %q", m.From())
	}
	if m.ResendClient() == nil || m.ResendClient().ApiKey != "re_kronos" {
		t.Fatalf("ResendClient = %+v", m.ResendClient())
	}
	id, err := m.Send(context.Background(), testMail("user@example.com"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "email_d" {
		t.Fatalf("id = %q", id)
	}
	if !strings.HasPrefix(stub.lastReq.Header.Get("Authorization"), "Bearer re_kronos") {
		t.Fatalf("Authorization = %q", stub.lastReq.Header.Get("Authorization"))
	}
}

func TestReloadResendProfiles(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "mail.json", `{
		"provider":"resend",
		"resend":{
			"default_profile":"kronos",
			"profiles":{
				"kronos":{"api_key":"re_k1","from_address":"a@kronos.example"},
				"other":{"api_key":"re_o","from_address":"o@x.example"}
			}
		}
	}`)

	fw := cf.New()
	addComponent(t, fw, cf_logs.New(cf_logs.WithWriter(io.Discard)))
	addComponent(t, fw, cf_configuration.New())
	m := New(WithConfigSource("mail", path))
	addComponent(t, fw, m)
	if err := fw.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = fw.Shutdown(context.Background()) })
	if got := m.ResendProfiles(); len(got) != 2 {
		t.Fatalf("profiles after Init = %v", got)
	}

	writeConfig(t, dir, "mail.json", `{
		"provider":"resend",
		"resend":{
			"default_profile":"kronos",
			"profiles":{
				"kronos":{"api_key":"re_k2","from_address":"b@kronos.example"}
			}
		}
	}`)
	conf, ok := cf.Get[*cf_configuration.Configuration](fw)
	if !ok {
		t.Fatal("configuration missing")
	}
	if err := conf.Reload("mail"); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := m.ResendProfiles(); len(got) != 1 || got[0] != "kronos" {
		t.Fatalf("profiles after reload = %v", got)
	}
	if m.From() != "b@kronos.example" {
		t.Fatalf("From() = %q", m.From())
	}
	if m.ResendClient() == nil || m.ResendClient().ApiKey != "re_k2" {
		t.Fatalf("client after reload = %+v", m.ResendClient())
	}
}

func TestSendUnisenderGo(t *testing.T) {
	stub := &stubRoundTripper{body: `{"status":"success","job_id":"1ZymBc-00041N-9X"}`, code: 200}
	m := New(WithProvider(ProviderUnisenderGo), WithUnisenderGoAPIKey("ug_key"),
		WithFromAddress("Brand <noreply@x.io>"),
		WithHTTPClient(&http.Client{Transport: stub}))
	if err := m.Init(context.Background(), cf.New()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	id, err := m.Send(context.Background(), testMail("user@example.com"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "1ZymBc-00041N-9X" {
		t.Fatalf("id = %q", id)
	}
	if stub.lastReq.Header.Get("X-API-KEY") != "ug_key" {
		t.Fatalf("X-API-KEY = %q", stub.lastReq.Header.Get("X-API-KEY"))
	}
	if !strings.HasSuffix(stub.lastReq.URL.Path, "/email/send.json") {
		t.Fatalf("path = %q", stub.lastReq.URL.Path)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stub.lastBody), &payload); err != nil {
		t.Fatalf("body: %v", err)
	}
	msg := payload["message"].(map[string]any)
	if msg["from_email"] != "noreply@x.io" || msg["from_name"] != "Brand" {
		t.Fatalf("from = %+v", msg)
	}
	if msg["skip_unsubscribe"] != float64(1) {
		t.Fatalf("skip_unsubscribe = %v (transactional default is 1)", msg["skip_unsubscribe"])
	}
}

func TestSendSES(t *testing.T) {
	stub := &stubRoundTripper{body: `{"MessageId":"010001-ses"}`, code: 200}
	m := New(
		WithProvider(ProviderSES),
		WithSESRegion("eu-central-1"),
		WithSESCredentials("AKIATEST", "secret"),
		WithSESEndpoint("https://ses.test"),
		WithFromAddress("noreply@x.io"),
		WithHTTPClient(&http.Client{Transport: stub}),
	)
	if err := m.Init(context.Background(), cf.New()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	if m.SESClient() == nil {
		t.Fatal("SESClient() should be non-nil")
	}

	id, err := m.Send(context.Background(), testMail("user@example.com"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "010001-ses" {
		t.Fatalf("id = %q", id)
	}
}

func TestSendValidation(t *testing.T) {
	m := New(WithProvider(ProviderResend), WithResendAPIKey("re_key"), WithFromAddress("noreply@x.io"))
	ctx := context.Background()
	if _, err := m.Send(ctx, testMail("a@x.io")); err == nil {
		t.Fatal("Send before Init should fail")
	}
	if err := m.Init(ctx, cf.New()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(ctx) })
	if _, err := m.Send(ctx, Mail{From: "a@x.io", To: []string{"a@x.io"}, Subject: "x"}); err == nil {
		t.Fatal("no HTML or Text should fail")
	}
	if _, err := m.Send(ctx, Mail{}); err == nil {
		t.Fatal("empty Mail should fail")
	} else if HTTPStatus(err) != 0 {
		t.Fatalf("validation error should not look like HTTP: %v", err)
	}
}

func TestSendNetworkNoRetry(t *testing.T) {
	stub := &stubRoundTripper{err: errors.New("connection refused")}
	m := New(WithProvider(ProviderResend), WithResendAPIKey("re_key"), WithFromAddress("noreply@x.io"),
		WithHTTPClient(&http.Client{Transport: stub}))
	if err := m.Init(context.Background(), cf.New()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	if _, err := m.Send(context.Background(), testMail("a@x.io")); err == nil {
		t.Fatal("Send should fail")
	} else if HTTPStatus(err) != 0 {
		t.Fatalf("HTTPStatus = %d, want 0", HTTPStatus(err))
	}
	if stub.calls != 1 {
		t.Fatalf("network must not retry, calls = %d", stub.calls)
	}
}

func TestSendRetryOn429(t *testing.T) {
	stub := &stubRoundTripper{
		codes:  []int{429, 200},
		bodies: []string{`{"message":"slow"}`, `{"id":"email_2"}`},
		header: http.Header{"Retry-After": []string{"0"}, "Content-Type": []string{"application/json"}},
	}
	m := New(WithProvider(ProviderResend), WithResendAPIKey("re_key"), WithFromAddress("noreply@x.io"),
		WithHTTPClient(&http.Client{Transport: stub}))
	if err := m.Init(context.Background(), cf.New()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	id, err := m.Send(context.Background(), testMail("a@x.io"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != "email_2" {
		t.Fatalf("id = %q", id)
	}
	if stub.calls != 2 {
		t.Fatalf("calls = %d, want 2", stub.calls)
	}
	if mtr := findMetric(t, m.Metrics(), "mail_send_retries_total", map[string]string{"component": "mail", "from": "noreply@x.io"}); mtr == nil || mtr.Value != 1 {
		t.Fatalf("retries = %+v", mtr)
	}
}

func TestReloadLastGood(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "mail.json", `{"provider":"resend","resend":{"api_key":"re_k1"},"from_address":"a@x.io"}`)

	fw := cf.New()
	addComponent(t, fw, cf_logs.New(cf_logs.WithWriter(io.Discard)))
	addComponent(t, fw, cf_configuration.New())
	m := New(WithConfigSource("mail", path))
	addComponent(t, fw, m)
	if err := fw.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = fw.Shutdown(context.Background()) })

	if m.ResendClient() == nil || m.ResendClient().ApiKey != "re_k1" {
		t.Fatalf("client after Init = %+v", m.ResendClient())
	}

	writeConfig(t, dir, "mail.json", `{"provider":"resend","resend":{"api_key":"re_k1","base_url":"://bad"},"from_address":"a@x.io"}`)
	conf, ok := cf.Get[*cf_configuration.Configuration](fw)
	if !ok {
		t.Fatal("configuration missing")
	}
	if err := conf.Reload("mail"); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if m.ResendClient() == nil || m.ResendClient().ApiKey != "re_k1" {
		t.Fatalf("last-good lost: %+v", m.ResendClient())
	}
	if m.From() != "a@x.io" {
		t.Fatalf("From after failed reload = %q", m.From())
	}
}

func TestReloadUpdatesSender(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "mail.json", `{"provider":"resend","resend":{"api_key":"re_k1"},"from_address":"a@x.io"}`)

	fw := cf.New()
	addComponent(t, fw, cf_logs.New(cf_logs.WithWriter(io.Discard)))
	addComponent(t, fw, cf_configuration.New())
	m := New(WithConfigSource("mail", path))
	addComponent(t, fw, m)
	if err := fw.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = fw.Shutdown(context.Background()) })

	writeConfig(t, dir, "mail.json", `{"provider":"resend","resend":{"api_key":"re_k2"},"from_address":"b@x.io"}`)
	conf, ok := cf.Get[*cf_configuration.Configuration](fw)
	if !ok {
		t.Fatal("configuration missing")
	}
	if err := conf.Reload("mail"); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if m.ResendClient() == nil || m.ResendClient().ApiKey != "re_k2" {
		t.Fatalf("client after reload = %+v", m.ResendClient())
	}
	if m.From() != "b@x.io" {
		t.Fatalf("From() = %q", m.From())
	}
	if n := m.reloads.Load(); n != 1 {
		t.Fatalf("reloads = %d", n)
	}
}

func TestInitializeRejectsEmptyFromAddress(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "mail.json", `{"provider":"resend","resend":{"api_key":"re_k1"}}`)

	fw := cf.New()
	addComponent(t, fw, cf_logs.New(cf_logs.WithWriter(io.Discard)))
	addComponent(t, fw, cf_configuration.New())
	m := New(WithConfigSource("mail", path))
	if err := fw.AddComponent(m); err != nil {
		t.Fatalf("AddComponent: %v", err)
	}
	if err := fw.Initialize(context.Background()); err == nil {
		t.Fatal("Initialize should fail when from_address is missing")
	}
}

func TestWithConfigSourceDeclaresConfigurationDependency(t *testing.T) {
	m := New(WithConfigSource("mail", ""))
	found := false
	for _, d := range m.GetDependencies() {
		if d == cf_configuration.ComponentName {
			found = true
		}
	}
	if !found {
		t.Fatalf("deps = %v", m.GetDependencies())
	}
}

func TestMetricsPlaceholder(t *testing.T) {
	m := New(WithProvider(ProviderResend), WithResendAPIKey("re_key"), WithFromAddress("noreply@x.io"))
	if err := m.Init(context.Background(), cf.New()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	base := map[string]string{"from": "noreply@x.io", "component": "mail"}
	if met := findMetric(t, m.Metrics(), "mail_emails_sent_total", base); met == nil || met.Value != 0 {
		t.Fatalf("sent placeholder = %+v", met)
	}
	if met := findMetric(t, m.Metrics(), "mail_info", map[string]string{"component": "mail", "provider": "resend", "from": "noreply@x.io"}); met == nil || met.Value != 1 {
		t.Fatalf("mail_info = %+v", met)
	}
}

func findMetric(t *testing.T, ms []cf_observability.Metric, name string, labels map[string]string) *cf_observability.Metric {
	t.Helper()
	for i := range ms {
		if ms[i].Name != name {
			continue
		}
		ok := true
		for k, v := range labels {
			if ms[i].Labels[k] != v {
				ok = false
				break
			}
		}
		if ok {
			return &ms[i]
		}
	}
	return nil
}
