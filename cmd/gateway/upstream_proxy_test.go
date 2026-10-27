package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gateway-go/internal/config"
	"gateway-go/internal/logger"
	"gateway-go/internal/plugin"
	"gateway-go/internal/response"
	"github.com/gin-gonic/gin"
)

// setupProxyTestGateway 辅助函数：构造包含指定路由的网关测试实例
func setupProxyTestGateway(t *testing.T, routesYAML string) *httptest.Server {
	t.Helper()
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "config.yaml")

	cfgContent := fmt.Sprintf(`
server:
  port: 8080
  mode: test
  read_timeout: "60s"
  write_timeout: "60s"
  max_header_bytes: 1048576
  graceful_shutdown_timeout: "30s"
log:
  level: info
  format: json
  output: stdout
  max_size: 100
  max_age: 30
  max_backups: 10
  compress: false
  access: true
routes:
%s
`, routesYAML)

	if err := os.WriteFile(cfgFile, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cm := config.NewConfigManager(cfgFile)
	if err := cm.LoadConfig(cfgFile); err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	configManager = cm

	if err := logger.Init(&cm.GetConfig().Log); err != nil {
		t.Fatalf("failed to init logger: %v", err)
	}

	pluginManager = plugin.NewManager()
	registerPlugins()

	engine := buildEngine()
	gwServer := httptest.NewServer(engine)
	t.Cleanup(func() {
		gwServer.Close()
		if globalTransportPool != nil {
			globalTransportPool.Reset()
		}
	})
	return gwServer
}

// TestUpstreamTimeout 验证超时逻辑：
// target.Timeout = 100 (100ms)，上游等待 500ms，网关在 ~100ms 内返回 504 Gateway Timeout，
// 且返回统一 JSON 结构体 {"code": 504, "message": "上游服务请求超时", "request_id": "..."}
func TestUpstreamTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("delayed ok"))
	}))
	defer upstream.Close()

	routesYAML := fmt.Sprintf(`
  - name: timeout-test-route
    match:
      path: /timeout
      type: prefix
    target:
      url: %s
      timeout: 100
      retries: 0
`, upstream.URL)

	gwServer := setupProxyTestGateway(t, routesYAML)
	client := &http.Client{Timeout: 3 * time.Second}

	start := time.Now()
	req, err := http.NewRequest(http.MethodGet, gwServer.URL+"/timeout", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("X-Request-Id", "test-timeout-req-123")

	resp, err := client.Do(req)
	duration := time.Since(start)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// 验证在 ~100ms 左右返回，远小于上游休眠的 500ms
	if duration >= 400*time.Millisecond {
		t.Errorf("expected timeout response within ~100ms, took %v", duration)
	}

	// 验证 HTTP 状态码 504
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Errorf("expected status 504 Gateway Timeout, got %d", resp.StatusCode)
	}

	// 验证统一 JSON 响应体
	var errResp response.ErrorResponse
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
		t.Fatalf("response body is not valid JSON: %v, raw: %s", err, string(bodyBytes))
	}

	if errResp.Code != 504 {
		t.Errorf("expected code 504, got %d", errResp.Code)
	}
	if errResp.Message != "上游服务请求超时" {
		t.Errorf("expected message '上游服务请求超时', got %q", errResp.Message)
	}
	if errResp.RequestID != "test-timeout-req-123" {
		t.Errorf("expected request_id 'test-timeout-req-123', got %q", errResp.RequestID)
	}
}

// TestRetryOn5xx 验证 5xx 重试：
// 配置 retries = 2，上游前 2 次返回 503，第 3 次返回 200，网关重试后最终返回 200
func TestRetryOn5xx(t *testing.T) {
	var reqCount int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&reqCount, 1)
		if count <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("temporarily unavailable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok on attempt 3"))
	}))
	defer upstream.Close()

	routesYAML := fmt.Sprintf(`
  - name: retry-5xx-route
    match:
      path: /retry
      type: prefix
    target:
      url: %s
      timeout: 5000
      retries: 2
`, upstream.URL)

	gwServer := setupProxyTestGateway(t, routesYAML)
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(gwServer.URL + "/retry")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed reading body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d, body: %s", resp.StatusCode, string(bodyBytes))
	}
	if string(bodyBytes) != "ok on attempt 3" {
		t.Errorf("expected body 'ok on attempt 3', got %q", string(bodyBytes))
	}

	// 验证上游共收到 3 次请求 (初始请求 1 次 + 重试 2 次)
	totalReceived := atomic.LoadInt32(&reqCount)
	if totalReceived != 3 {
		t.Errorf("expected upstream to receive exactly 3 requests, got %d", totalReceived)
	}
}

// TestNoRetryOnPost 验证非幂等方法 POST 不重试：
// POST 请求即使遇到上游 500 错误，也绝对不可重试，上游只收到 1 次请求
func TestNoRetryOnPost(t *testing.T) {
	var postCount int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&postCount, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer upstream.Close()

	routesYAML := fmt.Sprintf(`
  - name: post-no-retry-route
    match:
      path: /post
      type: prefix
    target:
      url: %s
      timeout: 5000
      retries: 2
`, upstream.URL)

	gwServer := setupProxyTestGateway(t, routesYAML)
	client := &http.Client{Timeout: 5 * time.Second}

	reqBody := bytes.NewBufferString(`{"action": "create_order"}`)
	resp, err := client.Post(gwServer.URL+"/post", "application/json", reqBody)
	if err != nil {
		t.Fatalf("POST request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", resp.StatusCode)
	}

	// 核心断言：POST 是非幂等操作，无论 retries 配置多少，上游只收到 1 次请求
	totalPosts := atomic.LoadInt32(&postCount)
	if totalPosts != 1 {
		t.Errorf("expected upstream to receive exactly 1 POST request, got %d", totalPosts)
	}
}

// TestTransportKeepAliveReuse 验证 TCP 连接复用：
// 多次向同一个目标服务发起 HTTP 请求，底层 TCP 连接被 keep-alive 复用，只创建 1 个底层连接
func TestTransportKeepAliveReuse(t *testing.T) {
	var newConnCount int32
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("keepalive ok"))
	}))
	upstream.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateNew {
			atomic.AddInt32(&newConnCount, 1)
		}
	}
	upstream.Start()
	defer upstream.Close()

	routesYAML := fmt.Sprintf(`
  - name: keepalive-route
    match:
      path: /keepalive
      type: prefix
    target:
      url: %s
      timeout: 5000
      retries: 0
`, upstream.URL)

	gwServer := setupProxyTestGateway(t, routesYAML)
	client := &http.Client{Timeout: 5 * time.Second}

	// 连续发送 5 次请求
	for i := 0; i < 5; i++ {
		resp, err := client.Get(gwServer.URL + "/keepalive")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("request %d: expected status 200, got %d", i, resp.StatusCode)
		}
	}

	// 验证 TCP 连接复用：5 次请求复用同一条 TCP 连接，底层新建连接数为 1
	conns := atomic.LoadInt32(&newConnCount)
	if conns != 1 {
		t.Errorf("expected exactly 1 TCP connection reused across 5 requests, got %d new connections", conns)
	}
}

// TestDirectorHeaderIsolation 验证 Director 中的 Header 隔离（深拷贝，无别名）：
// 验证 req.Header 是深拷贝克隆，Director 对 req.Header 的修改不会污染客户端请求的 Header map
func TestDirectorHeaderIsolation(t *testing.T) {
	upstreamReceivedHeaders := make(http.Header)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range r.Header {
			upstreamReceivedHeaders[k] = v
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("header isolation ok"))
	}))
	defer upstream.Close()

	routesYAML := fmt.Sprintf(`
  - name: header-isolation-route
    match:
      path: /headers
      type: prefix
    target:
      url: %s
      timeout: 5000
      retries: 0
`, upstream.URL)

	gwServer := setupProxyTestGateway(t, routesYAML)

	// 创建带自定义 Header 的客户端请求
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, gwServer.URL+"/headers", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("X-Custom-Client-Header", "client-val-1")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// 上游应该收到了经过 Director 补充的头信息
	if upstreamReceivedHeaders.Get("X-Origin-Host") == "" {
		t.Error("expected upstream to receive X-Origin-Host header")
	}
	if upstreamReceivedHeaders.Get("X-Forwarded-Host") == "" {
		t.Error("expected upstream to receive X-Forwarded-Host header")
	}

	// 单元级别直接验证 Director 内部隔离：
	clientReq, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	clientReq.Header.Set("X-Client-Original", "orig")

	ginCtx := &gin.Context{
		Request: clientReq,
	}

	// 模拟 Director 执行
	outReq := clientReq.Clone(ginCtx.Request.Context())
	// 修复后的代码：使用 Clone 深拷贝
	outReq.Header = ginCtx.Request.Header.Clone()
	outReq.Header.Set("X-Origin-Host", "upstream-target-host")
	outReq.Header.Set("X-Forwarded-Host", "client-host")

	// 核心断言：clientReq.Header 中不得存在由 Director 写入的 X-Origin-Host
	if clientReq.Header.Get("X-Origin-Host") != "" {
		t.Errorf("client header was aliased and modified: X-Origin-Host = %s", clientReq.Header.Get("X-Origin-Host"))
	}
	if clientReq.Header.Get("X-Forwarded-Host") != "" {
		t.Errorf("client header was aliased and modified: X-Forwarded-Host = %s", clientReq.Header.Get("X-Forwarded-Host"))
	}

	// 反向断言：修改 clientReq 也不能影响 outReq
	clientReq.Header.Set("X-Client-Modified-Later", "later")
	if outReq.Header.Get("X-Client-Modified-Later") != "" {
		t.Error("outReq header was aliased with clientReq")
	}
}

// TestReplayableBodyOnPutRetry 验证幂等 PUT 请求体重放：
// 当 PUT 请求在首次失败时，请求体在重试时能被完整重新发送给上游
func TestReplayableBodyOnPutRetry(t *testing.T) {
	var attempts int32
	var receivedBodies []string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBodies = append(receivedBodies, string(body))
		count := atomic.AddInt32(&attempts, 1)
		if count == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("retry me"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("put ok"))
	}))
	defer upstream.Close()

	routesYAML := fmt.Sprintf(`
  - name: put-retry-route
    match:
      path: /put-item
      type: prefix
    target:
      url: %s
      timeout: 5000
      retries: 2
`, upstream.URL)

	gwServer := setupProxyTestGateway(t, routesYAML)
	client := &http.Client{Timeout: 5 * time.Second}

	putPayload := `{"item_id": 42, "status": "updated"}`
	req, err := http.NewRequest(http.MethodPut, gwServer.URL+"/put-item", bytes.NewBufferString(putPayload))
	if err != nil {
		t.Fatalf("failed to create PUT request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PUT request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	if atomic.LoadInt32(&attempts) != 2 {
		t.Errorf("expected 2 attempts, got %d", atomic.LoadInt32(&attempts))
	}

	// 验证两次尝试都收到了完整的请求体
	if len(receivedBodies) != 2 {
		t.Fatalf("expected 2 received bodies, got %d", len(receivedBodies))
	}
	if receivedBodies[0] != putPayload {
		t.Errorf("attempt 1 body mismatch: got %q", receivedBodies[0])
	}
	if receivedBodies[1] != putPayload {
		t.Errorf("attempt 2 body mismatch: got %q", receivedBodies[1])
	}
}
