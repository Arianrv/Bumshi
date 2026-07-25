package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func render(t *testing.T, r *Registry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	r.Handler().ServeHTTP(rec, req)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("unexpected content-type %q", ct)
	}
	return rec.Body.String()
}

func mustContain(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Errorf("output missing %q\n---\n%s", want, body)
	}
}

func TestCounterVec(t *testing.T) {
	r := NewRegistry()
	c := NewCounterVec(r, "bumshi_http_requests_total", "reqs", "code")
	c.Inc("200")
	c.Inc("200")
	c.Inc("500")
	c.Add(-5, "200") // ignored: counters never decrease
	c.Inc("200", "extra") // ignored: label arity mismatch

	body := render(t, r)
	mustContain(t, body, "# TYPE bumshi_http_requests_total counter")
	mustContain(t, body, `bumshi_http_requests_total{code="200"} 2`)
	mustContain(t, body, `bumshi_http_requests_total{code="500"} 1`)
}

func TestGaugeVec(t *testing.T) {
	r := NewRegistry()
	g := NewGaugeVec(r, "bumshi_http_requests_in_flight", "inflight")
	g.Inc()
	g.Inc()
	g.Dec()

	body := render(t, r)
	mustContain(t, body, "# TYPE bumshi_http_requests_in_flight gauge")
	mustContain(t, body, "bumshi_http_requests_in_flight 1")
}

func TestHistogramVec(t *testing.T) {
	r := NewRegistry()
	h := NewHistogramVec(r, "lat_seconds", "latency", []float64{0.1, 1})
	h.Observe(0.05) // <= 0.1
	h.Observe(0.5)  // <= 1
	h.Observe(5)    // > 1, only +Inf

	body := render(t, r)
	mustContain(t, body, "# TYPE lat_seconds histogram")
	mustContain(t, body, `lat_seconds_bucket{le="0.1"} 1`)
	mustContain(t, body, `lat_seconds_bucket{le="1"} 2`)
	mustContain(t, body, `lat_seconds_bucket{le="+Inf"} 3`)
	mustContain(t, body, "lat_seconds_count 3")
}

func TestLabelValueEscaping(t *testing.T) {
	r := NewRegistry()
	c := NewCounterVec(r, "escaped_total", "help", "path")
	c.Inc(`a"b\c`)

	body := render(t, r)
	mustContain(t, body, `escaped_total{path="a\"b\\c"} 1`)
}
