package circuitbreaker

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gateway-go/internal/testutil"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestCircuitBreaker_Init(t *testing.T) {
	cbPlugin := New()
	defer cbPlugin.Stop()

	// Invalid config type
	err := cbPlugin.Init("invalid_string_config")
	if err == nil {
		t.Fatalf("expected error for invalid config type, got nil")
	}

	// Valid config
	validConfig := map[string]interface{}{
		"failure_threshold": 50,
		"recovery_timeout":  1,
		"half_open_quota":   2,
		"success_threshold": 50,
		"window_size":       5,
	}
	if err := cbPlugin.Init(validConfig); err != nil {
		t.Fatalf("unexpected error during init: %v", err)
	}
}

func TestCircuitBreaker_ThreeStateTransitions(t *testing.T) {
	cbPlugin := New()
	defer cbPlugin.Stop()

	cfg := map[string]interface{}{
		"failure_threshold": 50, // 50%
		"recovery_timeout":  1,  // 1 second
		"half_open_quota":   2,
		"success_threshold": 50,
		"window_size":       5,
	}
	if err := cbPlugin.Init(cfg); err != nil {
		t.Fatalf("failed to init: %v", err)
	}

	target := "http://service-demo"
	cb := cbPlugin.GetCircuitBreaker(target)

	// 1. Initial state must be Closed
	if atomic.LoadInt32(&cb.state) != int32(StateClosed) {
		t.Fatalf("expected initial state to be Closed, got %v", cb.state)
	}

	if !cb.allowRequest() {
		t.Fatalf("expected allowRequest to be true in Closed state")
	}

	// 2. Closed -> Open transition when failure threshold is exceeded
	// Record multiple failures
	for i := 0; i < 10; i++ {
		cb.recordFailure()
	}

	// allowRequest should detect failure rate >= 50% and flip to Open
	allowed := cb.allowRequest()
	if allowed {
		t.Fatalf("expected allowRequest to be false after failures")
	}
	if atomic.LoadInt32(&cb.state) != int32(StateOpen) {
		t.Fatalf("expected state to be Open, got %v", atomic.LoadInt32(&cb.state))
	}

	// 3. Open -> HalfOpen transition after recovery_timeout
	// In Open state, requests are blocked immediately
	if cb.allowRequest() {
		t.Fatalf("expected request blocked immediately in Open state")
	}

	// Fast-forward or sleep recovery_timeout
	time.Sleep(1100 * time.Millisecond)

	// Next call to allowRequest should transition from Open -> HalfOpen
	if !cb.allowRequest() {
		t.Fatalf("expected request allowed during transition to HalfOpen")
	}
	if atomic.LoadInt32(&cb.state) != int32(StateHalfOpen) {
		t.Fatalf("expected state to be HalfOpen, got %v", atomic.LoadInt32(&cb.state))
	}

	// 4. HalfOpen: allow requests up to halfOpenQuota
	if !cb.allowRequest() {
		t.Fatalf("expected first request allowed in HalfOpen")
	}
	if !cb.allowRequest() {
		t.Fatalf("expected second request allowed in HalfOpen")
	}
	// Next request in HalfOpen exceeds quota
	if cb.allowRequest() {
		t.Fatalf("expected request blocked in HalfOpen after quota exhausted")
	}

	// 5. HalfOpen -> Closed transition on success
	// Recording success in HalfOpen resets failure rate below success threshold
	for i := 0; i < 20; i++ {
		cb.recordSuccess()
	}
	if atomic.LoadInt32(&cb.state) != int32(StateClosed) {
		t.Fatalf("expected state to reset to Closed, got %v", atomic.LoadInt32(&cb.state))
	}
	if !cb.allowRequest() {
		t.Fatalf("expected requests allowed after transitioning back to Closed")
	}
}

func TestCircuitBreaker_ExecuteAndResponseWriter(t *testing.T) {
	cbPlugin := New()
	defer cbPlugin.Stop()

	cfg := map[string]interface{}{
		"failure_threshold": 50,
		"recovery_timeout":  5,
		"half_open_quota":   2,
		"success_threshold": 50,
		"window_size":       10,
	}
	if err := cbPlugin.Init(cfg); err != nil {
		t.Fatalf("failed to init: %v", err)
	}

	// 1. In Closed state, plugin wraps ResponseWriter
	w := httptest.NewRecorder()
	c, _ := testutil.NewTestContext(w)
	req, _ := http.NewRequest("GET", "/api/upstream", nil)
	c.Request = req
	c.Set("target", "http://backend-service")

	if err := cbPlugin.Execute(c); err != nil {
		t.Fatalf("unexpected error executing plugin: %v", err)
	}

	// Simulate backend writing 502 Bad Gateway
	c.Writer.WriteHeader(http.StatusBadGateway)
	c.Writer.WriteHeaderNow()
	if w.Code != http.StatusBadGateway {
		t.Errorf("expected status 502, got %d", w.Code)
	}

	// 2. Force circuit breaker to Open state
	cb := cbPlugin.GetCircuitBreaker("http://backend-service")
	cb.SetState(StateOpen)

	// Now execute plugin - should reject with 503 Service Unavailable
	wOpen := httptest.NewRecorder()
	cOpen, _ := testutil.NewTestContext(wOpen)
	reqOpen, _ := http.NewRequest("GET", "/api/upstream", nil)
	cOpen.Request = reqOpen
	cOpen.Set("target", "http://backend-service")

	if err := cbPlugin.Execute(cOpen); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	testutil.AssertStatus(t, wOpen, http.StatusServiceUnavailable)
	testutil.AssertJSON(t, wOpen.Body, map[string]interface{}{
		"code":    503,
		"message": "服务暂时不可用",
	})
}
