package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestGetRequestID(t *testing.T) {
	// 1. 从 context 获取
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	c.Request = req
	c.Set("request_id", "ctx-id-123")

	if got := GetRequestID(c); got != "ctx-id-123" {
		t.Fatalf("expected ctx-id-123, got %s", got)
	}

	// 2. 从 Header X-Request-Id 获取
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	req, _ = http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-Id", "hdr-id-456")
	c.Request = req

	if got := GetRequestID(c); got != "hdr-id-456" {
		t.Fatalf("expected hdr-id-456, got %s", got)
	}

	// 3. 从 Header X-Request-ID (大写) 获取
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	req, _ = http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "hdr-id-789")
	c.Request = req

	if got := GetRequestID(c); got != "hdr-id-789" {
		t.Fatalf("expected hdr-id-789, got %s", got)
	}

	// 4. 空情况
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	req, _ = http.NewRequest("GET", "/test", nil)
	c.Request = req

	if got := GetRequestID(c); got != "" {
		t.Fatalf("expected empty string, got %s", got)
	}
}

func TestRespondError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-Id", "req-test-1")
	c.Request = req

	RespondError(c, http.StatusForbidden, "IP 不在白名单中", "")

	if !c.IsAborted() {
		t.Errorf("expected context to be aborted")
	}

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status %d, got %d", http.StatusForbidden, w.Code)
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	expectedLen := strconv.Itoa(w.Body.Len())
	if cl := w.Header().Get("Content-Length"); cl != expectedLen {
		t.Errorf("expected Content-Length %s, got %s", expectedLen, cl)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal JSON response: %v", err)
	}

	if resp.Code != http.StatusForbidden {
		t.Errorf("expected code %d, got %d", http.StatusForbidden, resp.Code)
	}
	if resp.Message != "IP 不在白名单中" {
		t.Errorf("expected message 'IP 不在白名单中', got %s", resp.Message)
	}
	if resp.RequestID != "req-test-1" {
		t.Errorf("expected request_id 'req-test-1', got %s", resp.RequestID)
	}
}

func TestRespondErrorIdempotent(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	c.Request = req

	RespondError(c, http.StatusUnauthorized, "首次错误", "req-1")
	firstBody := w.Body.String()

	// 二次调用应该直接 return，不得重复写出
	RespondError(c, http.StatusInternalServerError, "二次错误", "req-2")

	if w.Body.String() != firstBody {
		t.Errorf("expected body to remain unchanged, got %s", w.Body.String())
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}
