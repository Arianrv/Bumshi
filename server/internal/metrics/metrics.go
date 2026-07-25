// Package metrics implements a small, dependency-free Prometheus text
// exposition registry: just enough for the control service's operational
// metrics. Keeping it in the standard library means the binary builds offline
// with no third-party modules. The public surface mirrors the common
// counter/gauge/histogram vector shape, so it can be swapped for
// prometheus/client_golang later without changing call sites.
package metrics

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// DefBuckets are the default latency histogram buckets, in seconds.
var DefBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Registry is a thread-safe, ordered collection of metric vectors.
type Registry struct {
	mu   sync.RWMutex
	vecs []vector
}

type vector interface {
	writeTo(b *strings.Builder)
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) register(v vector) {
	r.mu.Lock()
	r.vecs = append(r.vecs, v)
	r.mu.Unlock()
}

// Handler renders the registry in the Prometheus text exposition format.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var b strings.Builder
		r.mu.RLock()
		for _, v := range r.vecs {
			v.writeTo(&b)
		}
		r.mu.RUnlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(b.String()))
	})
}

// CounterVec is a set of monotonically increasing counters that share a name
// and label schema.
type CounterVec struct {
	name, help string
	labels     []string
	mu         sync.Mutex
	values     map[string]*counterChild
}

type counterChild struct {
	labelValues []string
	value       float64
}

// NewCounterVec creates and registers a counter vector.
func NewCounterVec(r *Registry, name, help string, labels ...string) *CounterVec {
	c := &CounterVec{name: name, help: help, labels: labels, values: map[string]*counterChild{}}
	r.register(c)
	return c
}

// Inc increments the counter identified by labelValues by one.
func (c *CounterVec) Inc(labelValues ...string) { c.Add(1, labelValues...) }

// Add increments the counter identified by labelValues by delta. Negative
// deltas and label-arity mismatches are ignored (counters never decrease).
func (c *CounterVec) Add(delta float64, labelValues ...string) {
	if len(labelValues) != len(c.labels) || delta < 0 {
		return
	}
	key := labelKey(labelValues)
	c.mu.Lock()
	child, ok := c.values[key]
	if !ok {
		child = &counterChild{labelValues: append([]string(nil), labelValues...)}
		c.values[key] = child
	}
	child.value += delta
	c.mu.Unlock()
}

func (c *CounterVec) writeTo(b *strings.Builder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	writeHeader(b, c.name, c.help, "counter")
	for _, child := range sortedChildren(c.values, func(x *counterChild) []string { return x.labelValues }) {
		b.WriteString(c.name)
		writeLabels(b, c.labels, child.labelValues)
		b.WriteByte(' ')
		b.WriteString(formatFloat(child.value))
		b.WriteByte('\n')
	}
}

// GaugeVec is a set of gauges (values that may go up or down) sharing a name
// and label schema.
type GaugeVec struct {
	name, help string
	labels     []string
	mu         sync.Mutex
	values     map[string]*gaugeChild
}

type gaugeChild struct {
	labelValues []string
	value       float64
}

// NewGaugeVec creates and registers a gauge vector.
func NewGaugeVec(r *Registry, name, help string, labels ...string) *GaugeVec {
	g := &GaugeVec{name: name, help: help, labels: labels, values: map[string]*gaugeChild{}}
	r.register(g)
	return g
}

// Set assigns v to the gauge identified by labelValues.
func (g *GaugeVec) Set(v float64, labelValues ...string) {
	g.mutate(labelValues, func(c *gaugeChild) { c.value = v })
}

// Add adds delta (which may be negative) to the gauge.
func (g *GaugeVec) Add(delta float64, labelValues ...string) {
	g.mutate(labelValues, func(c *gaugeChild) { c.value += delta })
}

// Inc adds one to the gauge.
func (g *GaugeVec) Inc(labelValues ...string) { g.Add(1, labelValues...) }

// Dec subtracts one from the gauge.
func (g *GaugeVec) Dec(labelValues ...string) { g.Add(-1, labelValues...) }

func (g *GaugeVec) mutate(labelValues []string, fn func(*gaugeChild)) {
	if len(labelValues) != len(g.labels) {
		return
	}
	key := labelKey(labelValues)
	g.mu.Lock()
	child, ok := g.values[key]
	if !ok {
		child = &gaugeChild{labelValues: append([]string(nil), labelValues...)}
		g.values[key] = child
	}
	fn(child)
	g.mu.Unlock()
}

func (g *GaugeVec) writeTo(b *strings.Builder) {
	g.mu.Lock()
	defer g.mu.Unlock()
	writeHeader(b, g.name, g.help, "gauge")
	for _, child := range sortedChildren(g.values, func(x *gaugeChild) []string { return x.labelValues }) {
		b.WriteString(g.name)
		writeLabels(b, g.labels, child.labelValues)
		b.WriteByte(' ')
		b.WriteString(formatFloat(child.value))
		b.WriteByte('\n')
	}
}

// HistogramVec is a set of cumulative histograms sharing a name, label schema,
// and bucket layout.
type HistogramVec struct {
	name, help string
	labels     []string
	buckets    []float64 // ascending upper bounds, excluding +Inf
	mu         sync.Mutex
	values     map[string]*histogramChild
}

type histogramChild struct {
	labelValues []string
	counts      []uint64 // cumulative per-bucket counts
	sum         float64
	count       uint64
}

// NewHistogramVec creates and registers a histogram vector. The buckets slice
// is copied and sorted ascending.
func NewHistogramVec(r *Registry, name, help string, buckets []float64, labels ...string) *HistogramVec {
	bs := append([]float64(nil), buckets...)
	sort.Float64s(bs)
	h := &HistogramVec{name: name, help: help, labels: labels, buckets: bs, values: map[string]*histogramChild{}}
	r.register(h)
	return h
}

// Observe records a single measurement v against the labelled histogram.
func (h *HistogramVec) Observe(v float64, labelValues ...string) {
	if len(labelValues) != len(h.labels) {
		return
	}
	key := labelKey(labelValues)
	h.mu.Lock()
	child, ok := h.values[key]
	if !ok {
		child = &histogramChild{
			labelValues: append([]string(nil), labelValues...),
			counts:      make([]uint64, len(h.buckets)),
		}
		h.values[key] = child
	}
	for i, ub := range h.buckets {
		if v <= ub {
			child.counts[i]++
		}
	}
	child.sum += v
	child.count++
	h.mu.Unlock()
}

func (h *HistogramVec) writeTo(b *strings.Builder) {
	h.mu.Lock()
	defer h.mu.Unlock()
	writeHeader(b, h.name, h.help, "histogram")
	for _, child := range sortedChildren(h.values, func(x *histogramChild) []string { return x.labelValues }) {
		for i, ub := range h.buckets {
			b.WriteString(h.name)
			b.WriteString("_bucket")
			writeBucketLabels(b, h.labels, child.labelValues, formatFloat(ub))
			b.WriteByte(' ')
			b.WriteString(strconv.FormatUint(child.counts[i], 10))
			b.WriteByte('\n')
		}
		b.WriteString(h.name)
		b.WriteString("_bucket")
		writeBucketLabels(b, h.labels, child.labelValues, "+Inf")
		b.WriteByte(' ')
		b.WriteString(strconv.FormatUint(child.count, 10))
		b.WriteByte('\n')

		b.WriteString(h.name)
		b.WriteString("_sum")
		writeLabels(b, h.labels, child.labelValues)
		b.WriteByte(' ')
		b.WriteString(formatFloat(child.sum))
		b.WriteByte('\n')

		b.WriteString(h.name)
		b.WriteString("_count")
		writeLabels(b, h.labels, child.labelValues)
		b.WriteByte(' ')
		b.WriteString(strconv.FormatUint(child.count, 10))
		b.WriteByte('\n')
	}
}

// HTTPCollectors bundles the standard HTTP server metrics.
type HTTPCollectors struct {
	Requests *CounterVec
	InFlight *GaugeVec
	Duration *HistogramVec
}

// NewHTTPCollectors registers and returns the standard HTTP server metrics.
func NewHTTPCollectors(r *Registry) *HTTPCollectors {
	return &HTTPCollectors{
		Requests: NewCounterVec(r, "bumshi_http_requests_total", "Total number of HTTP requests processed.", "method", "code"),
		InFlight: NewGaugeVec(r, "bumshi_http_requests_in_flight", "Number of HTTP requests currently being served."),
		Duration: NewHistogramVec(r, "bumshi_http_request_duration_seconds", "HTTP request latency in seconds.", DefBuckets, "method"),
	}
}

// --- shared helpers ---

func sortedChildren[T any](m map[string]*T, keys func(*T) []string) []*T {
	out := make([]*T, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		return labelKey(keys(out[i])) < labelKey(keys(out[j]))
	})
	return out
}

func labelKey(values []string) string {
	return strings.Join(values, "\x1f")
}

func writeLabels(b *strings.Builder, names, values []string) {
	if len(names) == 0 {
		return
	}
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(n)
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(values[i]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
}

func writeBucketLabels(b *strings.Builder, names, values []string, le string) {
	b.WriteByte('{')
	for i, n := range names {
		b.WriteString(n)
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(values[i]))
		b.WriteString(`",`)
	}
	b.WriteString(`le="`)
	b.WriteString(le)
	b.WriteString(`"}`)
}

func writeHeader(b *strings.Builder, name, help, typ string) {
	b.WriteString("# HELP ")
	b.WriteString(name)
	b.WriteByte(' ')
	b.WriteString(escapeHelp(help))
	b.WriteByte('\n')
	b.WriteString("# TYPE ")
	b.WriteString(name)
	b.WriteByte(' ')
	b.WriteString(typ)
	b.WriteByte('\n')
}

func escapeLabelValue(s string) string {
	if !strings.ContainsAny(s, "\\\"\n") {
		return s
	}
	return strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n").Replace(s)
}

func escapeHelp(s string) string {
	if !strings.ContainsAny(s, "\\\n") {
		return s
	}
	return strings.NewReplacer("\\", "\\\\", "\n", "\\n").Replace(s)
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
