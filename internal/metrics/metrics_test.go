package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func getCounterValue(m *prometheus.CounterVec, labelValues ...string) float64 {
	metric, err := m.GetMetricWithLabelValues(labelValues...)
	if err != nil {
		return 0
	}
	var pb dto.Metric
	if err := metric.Write(&pb); err != nil {
		return 0
	}
	return pb.GetCounter().GetValue()
}

func getGaugeValue(m *prometheus.GaugeVec, labelValues ...string) float64 {
	metric, err := m.GetMetricWithLabelValues(labelValues...)
	if err != nil {
		return 0
	}
	var pb dto.Metric
	if err := metric.Write(&pb); err != nil {
		return 0
	}
	return pb.GetGauge().GetValue()
}

func getHistogramCount(m *prometheus.HistogramVec, labelValues ...string) uint64 {
	observer, err := m.GetMetricWithLabelValues(labelValues...)
	if err != nil {
		return 0
	}
	metric, ok := observer.(prometheus.Metric)
	if !ok {
		return 0
	}
	var pb dto.Metric
	if err := metric.Write(&pb); err != nil {
		return 0
	}
	return pb.GetHistogram().GetSampleCount()
}

func TestMetricsRecording(t *testing.T) {
	Reset()

	// 1. RecordRequest & RecordRequestStatus
	RecordRequest("api_v1", "GET", "200")
	RecordRequest("api_v1", "GET", "200")
	RecordRequestStatus("api_v1", "POST", 201)

	if val := getCounterValue(RequestsTotal, "api_v1", "GET", "200"); val != 2 {
		t.Fatalf("expected RequestsTotal 2, got %v", val)
	}
	if val := getCounterValue(RequestsTotal, "api_v1", "POST", "201"); val != 1 {
		t.Fatalf("expected RequestsTotal 1, got %v", val)
	}

	// 2. RecordUpstreamLatency
	RecordUpstreamLatency("api_v1", 25*time.Millisecond)
	RecordUpstreamLatency("api_v1", 100*time.Millisecond)
	if count := getHistogramCount(UpstreamRTSeconds, "api_v1"); count != 2 {
		t.Fatalf("expected UpstreamRTSeconds sample count 2, got %d", count)
	}

	// 3. RecordRateLimitRejected
	RecordRateLimitRejected("login_route")
	if val := getCounterValue(RateLimitRejectedTotal, "login_route"); val != 1 {
		t.Fatalf("expected RateLimitRejectedTotal 1, got %v", val)
	}

	// 4. RecordCircuitOpen
	RecordCircuitOpen("payment_route")
	if val := getCounterValue(CircuitOpenTotal, "payment_route"); val != 1 {
		t.Fatalf("expected CircuitOpenTotal 1, got %v", val)
	}

	// 5. RecordReload
	RecordReload("success")
	RecordReload("failure")
	if val := getCounterValue(ReloadTotal, "success"); val != 1 {
		t.Fatalf("expected ReloadTotal[success] 1, got %v", val)
	}
	if val := getCounterValue(ReloadTotal, "failure"); val != 1 {
		t.Fatalf("expected ReloadTotal[failure] 1, got %v", val)
	}

	// 6. SetConfigVersion
	SetConfigVersion("v1.2.3")
	if val := getGaugeValue(ConfigVersion, "v1.2.3"); val != 1 {
		t.Fatalf("expected ConfigVersion[v1.2.3] 1, got %v", val)
	}
	// Update version and ensure old version reset
	SetConfigVersion("v1.2.4")
	if val := getGaugeValue(ConfigVersion, "v1.2.4"); val != 1 {
		t.Fatalf("expected ConfigVersion[v1.2.4] 1, got %v", val)
	}
	if val := getGaugeValue(ConfigVersion, "v1.2.3"); val != 0 {
		t.Fatalf("expected ConfigVersion[v1.2.3] 0 after reset, got %v", val)
	}
}

func TestMetricsDefaults(t *testing.T) {
	Reset()

	RecordRequest("", "", "")
	if val := getCounterValue(RequestsTotal, "unknown", "-", "unknown"); val != 1 {
		t.Fatalf("expected default RequestsTotal 1, got %v", val)
	}

	RecordUpstreamLatency("", 10*time.Millisecond)
	if count := getHistogramCount(UpstreamRTSeconds, "unknown"); count != 1 {
		t.Fatalf("expected default UpstreamRTSeconds count 1, got %d", count)
	}

	RecordRateLimitRejected("")
	if val := getCounterValue(RateLimitRejectedTotal, "unknown"); val != 1 {
		t.Fatalf("expected default RateLimitRejectedTotal 1, got %v", val)
	}

	RecordCircuitOpen("")
	if val := getCounterValue(CircuitOpenTotal, "unknown"); val != 1 {
		t.Fatalf("expected default CircuitOpenTotal 1, got %v", val)
	}

	RecordReload("")
	if val := getCounterValue(ReloadTotal, "unknown"); val != 1 {
		t.Fatalf("expected default ReloadTotal 1, got %v", val)
	}

	SetConfigVersion("")
	if val := getGaugeValue(ConfigVersion, "unknown"); val != 1 {
		t.Fatalf("expected default ConfigVersion 1, got %v", val)
	}
}
