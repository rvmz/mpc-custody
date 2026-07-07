// Package observability provides metrics, tracing, and request instrumentation.
package observability

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Metrics stores counters and latency summaries exposed in Prometheus text format.
type Metrics struct {
	mu        sync.RWMutex
	counters  map[string]float64
	latencies map[string][]time.Duration
}

// NewMetrics creates an empty metrics registry.
func NewMetrics() *Metrics {
	return &Metrics{
		counters:  make(map[string]float64),
		latencies: make(map[string][]time.Duration),
	}
}

// Inc increments a named counter with label values.
func (m *Metrics) Inc(name string, labels map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[metricKey(name, labels)]++
}

// ObserveDuration records a latency sample.
func (m *Metrics) ObserveDuration(name string, labels map[string]string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := metricKey(name, labels)
	m.latencies[key] = append(m.latencies[key], duration)
}

// WritePrometheus writes metrics in Prometheus exposition format.
func (m *Metrics) WritePrometheus(w io.Writer) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	counterKeys := make([]string, 0, len(m.counters))
	for key := range m.counters {
		counterKeys = append(counterKeys, key)
	}
	sort.Strings(counterKeys)

	for _, key := range counterKeys {
		fmt.Fprintf(w, "%s %g\n", key, m.counters[key])
	}

	latencyKeys := make([]string, 0, len(m.latencies))
	for key := range m.latencies {
		latencyKeys = append(latencyKeys, key)
	}
	sort.Strings(latencyKeys)

	for _, key := range latencyKeys {
		var total time.Duration
		for _, sample := range m.latencies[key] {
			total += sample
		}
		count := len(m.latencies[key])
		if count == 0 {
			continue
		}
		fmt.Fprintf(w, "%s %d\n", sampleKey(key, "_count"), count)
		fmt.Fprintf(w, "%s %g\n", sampleKey(key, "_sum"), total.Seconds())
	}
}

func metricKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}

	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.ReplaceAll(labels[key], `"`, `\"`)
		parts = append(parts, fmt.Sprintf(`%s="%s"`, key, value))
	}
	return fmt.Sprintf("%s{%s}", name, strings.Join(parts, ","))
}

func sampleKey(key string, suffix string) string {
	labelStart := strings.IndexByte(key, '{')
	if labelStart == -1 {
		return key + suffix
	}
	return key[:labelStart] + suffix + key[labelStart:]
}
