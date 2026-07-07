// Package observability verifies telemetry helpers.
package observability

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestWritePrometheusLatencyLabelsUseValidSampleNames(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveDuration("custody_http_request_duration_seconds", map[string]string{"route": "/v1/wallets"}, time.Second)

	var output bytes.Buffer
	metrics.WritePrometheus(&output)

	body := output.String()
	if !strings.Contains(body, `custody_http_request_duration_seconds_count{route="/v1/wallets"} 1`) {
		t.Fatalf("missing valid count sample: %s", body)
	}
	if strings.Contains(body, `{route="/v1/wallets"}_count`) {
		t.Fatalf("contains invalid labeled sample name: %s", body)
	}
}
