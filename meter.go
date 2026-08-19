package cf_mail

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// fromCtxKey is the request-context key carrying the resolved sender address so
// the transport can attribute each send to its actual From.
type fromCtxKey struct{}

// unknownFrom is the attribution bucket for sends that bypass Send() and call
// a provider SDK directly (the context then carries no from).
const unknownFrom = "unknown"

// sendMeter tallies send outcomes and latency per sender at the transport
// layer. Counters are cumulative and survive client rebuilds on config reload.
type sendMeter struct {
	mu      sync.Mutex
	senders map[string]*senderStats // from -> stats
}

type senderStats struct {
	sent          uint64
	failed        map[string]uint64 // error_code -> count
	durationSum   float64
	durationCount uint64
	retries       uint64
}

func newSendMeter() *sendMeter {
	return &sendMeter{senders: make(map[string]*senderStats)}
}

func (m *sendMeter) addRetry(from string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.senders[from]
	if s == nil {
		s = &senderStats{failed: make(map[string]uint64)}
		m.senders[from] = s
	}
	s.retries++
}

func (m *sendMeter) snapshot() map[string]senderStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]senderStats, len(m.senders))
	for from, s := range m.senders {
		failed := make(map[string]uint64, len(s.failed))
		for code, n := range s.failed {
			failed[code] = n
		}
		out[from] = senderStats{sent: s.sent, failed: failed, durationSum: s.durationSum, durationCount: s.durationCount, retries: s.retries}
	}
	return out
}

// meterTransport wraps a base RoundTripper and records outcomes per sender:
// HTTP 2xx is a sent email, any other response status is a failure keyed by
// the status code, and a transport error is a failure keyed "network".
type meterTransport struct {
	base  http.RoundTripper
	meter *sendMeter
}

func (t *meterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	from, _ := req.Context().Value(fromCtxKey{}).(string)
	if from == "" {
		from = unknownFrom
	}
	start := time.Now()
	resp, err := base.RoundTrip(req)
	d := time.Since(start).Seconds()
	if slot, ok := req.Context().Value(httpStatusKey{}).(*httpStatusSlot); ok && slot != nil {
		if err != nil {
			slot.code = 0
			slot.retryAfter = ""
		} else if resp != nil {
			slot.code = resp.StatusCode
			slot.retryAfter = resp.Header.Get("Retry-After")
		}
	}
	t.meter.mu.Lock()
	s := t.meter.senders[from]
	if s == nil {
		s = &senderStats{failed: make(map[string]uint64)}
		t.meter.senders[from] = s
	}
	s.durationSum += d
	s.durationCount++
	switch {
	case err != nil:
		s.failed["network"]++
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		s.sent++
	default:
		s.failed[strconv.Itoa(resp.StatusCode)]++
	}
	t.meter.mu.Unlock()
	return resp, err
}

func (c *CFMail) meteredClient() *http.Client {
	hc := c.httpClient
	timeout := c.timeout
	var transport http.RoundTripper
	if hc != nil {
		transport = hc.Transport
		if hc.Timeout > 0 {
			timeout = hc.Timeout
		}
	}
	return &http.Client{
		Transport: &meterTransport{base: transport, meter: c.meter},
		Timeout:   timeout,
	}
}
