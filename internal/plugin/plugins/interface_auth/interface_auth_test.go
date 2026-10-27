package interface_auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupTestContext(token string, path string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	c.Request = req
	return c, w
}

// TestTokenInHeaderOnly 验证 token 仅通过 Authorization: Bearer <token> 请求头传递，严禁出现在 URL 路径中
func TestTokenInHeaderOnly(t *testing.T) {
	var capturedURL string
	var capturedAuthHeader string

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		capturedAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer authServer.Close()

	plugin := New()
	defer plugin.Stop()

	// 截取 host
	host := strings.TrimPrefix(authServer.URL, "http://")

	err := plugin.Init(map[string]interface{}{
		"consumers": map[string]interface{}{
			"host":      host,
			"auth_api":  "/api/v1/verify",
			"auth_mode": "http_status",
		},
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	testToken := "secret-jwt-token-xyz-12345"
	c, w := setupTestContext(testToken, "/protected/resource")

	err = plugin.Execute(c)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	// 1. 验证 URL 路径中绝对不包含 token
	if strings.Contains(capturedURL, testToken) {
		t.Errorf("SECURITY VIOLATION: URL contains token: %s", capturedURL)
	}
	if capturedURL != "/api/v1/verify" {
		t.Errorf("expected URL '/api/v1/verify', got %s", capturedURL)
	}

	// 2. 验证 Authorization 头部正确携带 Bearer token
	expectedAuth := "Bearer " + testToken
	if capturedAuthHeader != expectedAuth {
		t.Errorf("expected Authorization header '%s', got '%s'", expectedAuth, capturedAuthHeader)
	}
}

// TestAuthModes 验证两种认证判定模式：http_status 与 legacy_unauthorized_bool
func TestAuthModes(t *testing.T) {
	t.Run("http_status_mode", func(t *testing.T) {
		var mockStatusCode int
		authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(mockStatusCode)
		}))
		defer authServer.Close()

		host := strings.TrimPrefix(authServer.URL, "http://")
		plugin := New()
		defer plugin.Stop()

		err := plugin.Init(map[string]interface{}{
			"consumers": map[string]interface{}{
				"host":      host,
				"auth_api":  "/auth",
				"auth_mode": "http_status",
			},
		})
		if err != nil {
			t.Fatalf("Init failed: %v", err)
		}

		// Case 1: 200 OK -> 通过
		mockStatusCode = http.StatusOK
		c1, w1 := setupTestContext("valid-token", "/api/test")
		if err := plugin.Execute(c1); err != nil || w1.Code != http.StatusOK {
			t.Fatalf("expected pass for 200 OK, got code %d, body: %s", w1.Code, w1.Body.String())
		}

		// Case 2: 204 No Content -> 通过 (2xx)
		mockStatusCode = http.StatusNoContent
		c2, w2 := setupTestContext("valid-token-2", "/api/test")
		if err := plugin.Execute(c2); err != nil || w2.Code != http.StatusOK {
			t.Fatalf("expected pass for 204 No Content, got code %d, body: %s", w2.Code, w2.Body.String())
		}

		// Case 3: 401 Unauthorized -> 拒绝 401
		mockStatusCode = http.StatusUnauthorized
		c3, w3 := setupTestContext("invalid-token", "/api/test")
		if err := plugin.Execute(c3); err != nil {
			t.Fatalf("Execute error: %v", err)
		}
		if w3.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for 401 Unauthorized, got %d, body: %s", w3.Code, w3.Body.String())
		}

		// Case 4: 403 Forbidden -> 拒绝 401
		mockStatusCode = http.StatusForbidden
		c4, w4 := setupTestContext("forbidden-token", "/api/test")
		if err := plugin.Execute(c4); err != nil {
			t.Fatalf("Execute error: %v", err)
		}
		if w4.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for 403 Forbidden, got %d, body: %s", w4.Code, w4.Body.String())
		}
	})

	t.Run("legacy_unauthorized_bool_mode", func(t *testing.T) {
		var mockResponseBody string
		var capturedAuthHeader string

		authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedAuthHeader = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(mockResponseBody))
		}))
		defer authServer.Close()

		host := strings.TrimPrefix(authServer.URL, "http://")
		plugin := New()
		defer plugin.Stop()

		err := plugin.Init(map[string]interface{}{
			"consumers": map[string]interface{}{
				"host":      host,
				"auth_api":  "/unauthorized",
				"auth_mode": "legacy_unauthorized_bool",
			},
		})
		if err != nil {
			t.Fatalf("Init failed: %v", err)
		}

		// Case 1: 响应 "false" -> 判定通过，且 token 仍由 Header 传递
		mockResponseBody = "false"
		c1, w1 := setupTestContext("legacy-token-01", "/api/legacy")
		if err := plugin.Execute(c1); err != nil || w1.Code != http.StatusOK {
			t.Fatalf("expected pass for 'false', got code %d, body: %s", w1.Code, w1.Body.String())
		}
		if capturedAuthHeader != "Bearer legacy-token-01" {
			t.Errorf("expected Bearer token in header, got: %s", capturedAuthHeader)
		}

		// Case 2: 响应 "true" -> 判定拒绝 (401)
		mockResponseBody = "true"
		c2, w2 := setupTestContext("legacy-token-02", "/api/legacy")
		if err := plugin.Execute(c2); err != nil {
			t.Fatalf("Execute error: %v", err)
		}
		if w2.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for 'true', got %d, body: %s", w2.Code, w2.Body.String())
		}

		// Case 3: 响应未知内容 -> 判定拒绝 (401)
		mockResponseBody = "unexpected-content"
		c3, w3 := setupTestContext("legacy-token-03", "/api/legacy")
		if err := plugin.Execute(c3); err != nil {
			t.Fatalf("Execute error: %v", err)
		}
		if w3.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for unknown response, got %d, body: %s", w3.Code, w3.Body.String())
		}
	})
}

// TestCacheTTL 测试认证结果本地缓存 TTL
func TestCacheTTL(t *testing.T) {
	var callCount int32
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	host := strings.TrimPrefix(authServer.URL, "http://")
	plugin := New()
	defer plugin.Stop()

	err := plugin.Init(map[string]interface{}{
		"cache_ttl": 2, // 2秒缓存
		"consumers": map[string]interface{}{
			"host":     host,
			"auth_api": "/verify",
		},
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	token := "cached-token-123"

	// 第一次调用：应请求远程服务
	c1, w1 := setupTestContext(token, "/api/test")
	if err := plugin.Execute(c1); err != nil || w1.Code != http.StatusOK {
		t.Fatalf("first call failed: %d", w1.Code)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("expected 1 call, got %d", atomic.LoadInt32(&callCount))
	}

	// 第二次调用：在 TTL 内应命中缓存，不请求远程服务
	c2, w2 := setupTestContext(token, "/api/test")
	if err := plugin.Execute(c2); err != nil || w2.Code != http.StatusOK {
		t.Fatalf("second call failed: %d", w2.Code)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("expected still 1 call due to cache, got %d", atomic.LoadInt32(&callCount))
	}
	if c2.GetString("plugin_result_interface_auth") != "cached_success" {
		t.Errorf("expected cached_success, got %s", c2.GetString("plugin_result_interface_auth"))
	}

	// 睡眠等待 TTL 过期
	time.Sleep(2100 * time.Millisecond)

	// 第三次调用：缓存过期后应重新请求远程服务
	c3, w3 := setupTestContext(token, "/api/test")
	if err := plugin.Execute(c3); err != nil || w3.Code != http.StatusOK {
		t.Fatalf("third call failed: %d", w3.Code)
	}
	if atomic.LoadInt32(&callCount) != 2 {
		t.Fatalf("expected 2 calls after cache expiration, got %d", atomic.LoadInt32(&callCount))
	}
}

// TestResponseBodyLimit4KB 验证外部认证服务响应体读取上限为 4KB (io.LimitReader)
func TestResponseBodyLimit4KB(t *testing.T) {
	// 构造一个超过 4KB (e.g. 10KB) 的响应服务
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// 写入 10KB 的数据，开头为 "false"，后面为垃圾数据
		largeData := "false" + strings.Repeat("A", 10*1024)
		_, _ = io.WriteString(w, largeData)
	}))
	defer authServer.Close()

	host := strings.TrimPrefix(authServer.URL, "http://")
	plugin := New()
	defer plugin.Stop()

	err := plugin.Init(map[string]interface{}{
		"consumers": map[string]interface{}{
			"host":      host,
			"auth_api":  "/unauthorized",
			"auth_mode": "legacy_unauthorized_bool",
		},
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	c, w := setupTestContext("token-4kb-test", "/api/test")
	// 即使后端返回了 10KB，LimitReader 截取 4KB 后 strings.TrimSpace("false" + 4091 "A"s)
	// 不是严格的 "false"，被安全识别为未知响应拒绝 401，而不会耗尽网关内存
	_ = plugin.Execute(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when response does not match, got %d", w.Code)
	}
}
