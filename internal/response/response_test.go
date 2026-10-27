package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestGenerateRequestID(t *testing.T) {
	id1 := GenerateRequestID()
	id2 := GenerateRequestID()

	if id1 == "" || id2 == "" {
		t.Fatal("GenerateRequestID returned empty string")
	}
	if id1 == id2 {
		t.Fatalf("expected unique IDs, got duplicate: %s", id1)
	}
	// Check UUID format (36 chars with 4 dashes)
	if len(id1) != 36 || strings.Count(id1, "-") != 4 {
		t.Fatalf("expected UUID format (len 36 with 4 dashes), got %s", id1)
	}
}

func TestRequestIDMiddleware_Generated(t *testing.T) {
	r := gin.New()
	r.Use(RequestIDMiddleware())
	var capturedCtxID string
	r.GET("/test", func(c *gin.Context) {
		capturedCtxID = c.GetString("request_id")
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	respHdrID := w.Header().Get("X-Request-ID")
	if respHdrID == "" {
		t.Fatal("expected X-Request-ID response header, got empty")
	}
	if capturedCtxID == "" {
		t.Fatal("expected request_id in gin.Context, got empty")
	}
	if respHdrID != capturedCtxID {
		t.Fatalf("expected matching header (%s) and context ID (%s)", respHdrID, capturedCtxID)
	}
}

func TestRequestIDMiddleware_Preserved(t *testing.T) {
	testCases := []struct {
		name       string
		headerKey  string
		headerVal  string
		expectedID string
	}{
		{
			name:       "X-Request-ID",
			headerKey:  "X-Request-ID",
			headerVal:  "client-id-custom-123",
			expectedID: "client-id-custom-123",
		},
		{
			name:       "lowercase x-request-id",
			headerKey:  "x-request-id",
			headerVal:  "lower-id-custom-456",
			expectedID: "lower-id-custom-456",
		},
		{
			name:       "X-Request-Id",
			headerKey:  "X-Request-Id",
			headerVal:  "mixed-id-custom-789",
			expectedID: "mixed-id-custom-789",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(RequestIDMiddleware())
			var capturedCtxID string
			r.GET("/test", func(c *gin.Context) {
				capturedCtxID = c.GetString("request_id")
				c.String(http.StatusOK, "ok")
			})

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/test", nil)
			req.Header.Set(tc.headerKey, tc.headerVal)
			r.ServeHTTP(w, req)

			respHdrID := w.Header().Get("X-Request-ID")
			if respHdrID != tc.expectedID {
				t.Fatalf("expected X-Request-ID %q, got %q", tc.expectedID, respHdrID)
			}
			if capturedCtxID != tc.expectedID {
				t.Fatalf("expected context request_id %q, got %q", tc.expectedID, capturedCtxID)
			}
		})
	}
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

	if rid := w.Header().Get("X-Request-ID"); rid != "req-test-1" {
		t.Errorf("expected X-Request-ID req-test-1, got %s", rid)
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
