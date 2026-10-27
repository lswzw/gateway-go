package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gateway-go/internal/config"
	"gateway-go/internal/logger"
	"gateway-go/internal/plugin"
)

// setupGatewayTestEnv 辅助函数：初始化测试网关环境并返回网关测试服务
func setupGatewayTestEnv(t *testing.T, cfgContent string) (*httptest.Server, string) {
	t.Helper()

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "gateway_access.log")
	cfgFile := filepath.Join(tmpDir, "config.yaml")

	fullCfg := fmt.Sprintf(cfgContent, logFile)
	if err := os.WriteFile(cfgFile, []byte(fullCfg), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
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
	return gwServer, logFile
}

// readAccessLogEntries 从日志文件中解析所有的 access 日志条目并验证合法 JSON 格式
func readAccessLogEntries(t *testing.T, logFile string) []map[string]interface{} {
	t.Helper()

	_ = logger.Sync()
	data, err := os.ReadFile(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("failed to read log file %s: %v", logFile, err)
	}

	var accessEntries []map[string]interface{}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for lineIdx, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d is not valid JSON: %v, raw: %s", lineIdx, err, line)
		}
		if entry["msg"] == "access" {
			accessEntries = append(accessEntries, entry)
		}
	}
	return accessEntries
}

// TestAccessLogLifecycleTimingAndFormat 验证访问日志计时 (rt_ms)、字段与合法 JSON (REQ-P0-05)
func TestAccessLogLifecycleTimingAndFormat(t *testing.T) {
	delayMs := 50 * time.Millisecond
	fakeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delayMs)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"delayed_ok"}`))
	}))
	defer fakeUpstream.Close()

	cfgTemplate := `
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
  output: %s
  max_size: 100
  max_age: 30
  max_backups: 10
  compress: false
  access: true
routes:
  - name: delay-route
    match:
      path: /delay
      type: prefix
    target:
      url: ` + fakeUpstream.URL + `
`

	gwServer, logFile := setupGatewayTestEnv(t, cfgTemplate)
	defer gwServer.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, gwServer.URL+"/delay", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("X-Request-ID", "req-test-timing-12345")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "delayed_ok") {
		t.Fatalf("unexpected response body: %s", string(body))
	}

	entries := readAccessLogEntries(t, logFile)
	if len(entries) == 0 {
		t.Fatalf("expected at least 1 access log entry, got 0")
	}

	entry := entries[len(entries)-1]

	// 1. 验证基础 Zap 规范字段: level="info", msg="access", time 存在
	if entry["level"] != "info" {
		t.Errorf("expected level 'info', got %v", entry["level"])
	}
	if entry["msg"] != "access" {
		t.Errorf("expected msg 'access', got %v", entry["msg"])
	}
	if _, ok := entry["time"]; !ok {
		t.Errorf("expected time field in access log")
	}

	// 2. 验证业务核心字段: status, bytes, upstream, rt_ms, request_id, client_ip, error_kind, method, path
	if entry["status"] != float64(200) {
		t.Errorf("expected status 200, got %v", entry["status"])
	}
	if entry["upstream"] != fakeUpstream.URL {
		t.Errorf("expected upstream %s, got %v", fakeUpstream.URL, entry["upstream"])
	}
	if entry["method"] != "GET" {
		t.Errorf("expected method 'GET', got %v", entry["method"])
	}
	if entry["path"] != "/delay" {
		t.Errorf("expected path '/delay', got %v", entry["path"])
	}
	if entry["request_id"] != "req-test-timing-12345" {
		t.Errorf("expected request_id 'req-test-timing-12345', got %v", entry["request_id"])
	}
	if entry["error_kind"] != "-" {
		t.Errorf("expected error_kind '-', got %v", entry["error_kind"])
	}
	if entry["client_ip"] == "" || entry["client_ip"] == "-" {
		t.Errorf("expected non-empty client_ip, got %v", entry["client_ip"])
	}
	bytesVal, ok := entry["bytes"].(float64)
	if !ok || bytesVal <= 0 {
		t.Errorf("expected bytes > 0, got %v", entry["bytes"])
	}

	// 3. 验证生命周期耗时 rt_ms 准确反映上游延迟 (>= 45ms)
	rtMsVal, ok := entry["rt_ms"].(float64)
	if !ok {
		t.Fatalf("expected rt_ms to be numeric, got %v", entry["rt_ms"])
	}
	if rtMsVal < 45 {
		t.Errorf("expected rt_ms >= 45 (actual delay ~50ms), got %f", rtMsVal)
	}
}

// TestAccessLogDisabledByConfig 验证配置 log.access: false 时禁用访问日志
func TestAccessLogDisabledByConfig(t *testing.T) {
	fakeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer fakeUpstream.Close()

	cfgTemplate := `
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
  output: %s
  max_size: 100
  max_age: 30
  max_backups: 10
  compress: false
  access: false
routes:
  - name: test-route
    match:
      path: /test
      type: prefix
    target:
      url: ` + fakeUpstream.URL + `
`

	gwServer, logFile := setupGatewayTestEnv(t, cfgTemplate)
	defer gwServer.Close()

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(gwServer.URL + "/test")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	entries := readAccessLogEntries(t, logFile)
	if len(entries) > 0 {
		t.Fatalf("expected 0 access log entries when access: false, got %d", len(entries))
	}
}

// TestAccessLogFieldFiltering 验证配置 log.access_fields 白名单时仅输出指定字段
func TestAccessLogFieldFiltering(t *testing.T) {
	fakeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("filtered"))
	}))
	defer fakeUpstream.Close()

	cfgTemplate := `
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
  output: %s
  max_size: 100
  max_age: 30
  max_backups: 10
  compress: false
  access: true
  access_fields:
    - status
    - path
    - rt_ms
    - upstream
routes:
  - name: filter-route
    match:
      path: /filtered
      type: prefix
    target:
      url: ` + fakeUpstream.URL + `
`

	gwServer, logFile := setupGatewayTestEnv(t, cfgTemplate)
	defer gwServer.Close()

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(gwServer.URL + "/filtered")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	entries := readAccessLogEntries(t, logFile)
	if len(entries) == 0 {
		t.Fatalf("expected at least 1 access log entry")
	}

	entry := entries[len(entries)-1]

	// 包含白名单指定的字段
	if entry["status"] != float64(200) {
		t.Errorf("expected status 200, got %v", entry["status"])
	}
	if entry["path"] != "/filtered" {
		t.Errorf("expected path '/filtered', got %v", entry["path"])
	}
	if entry["upstream"] != fakeUpstream.URL {
		t.Errorf("expected upstream %s, got %v", fakeUpstream.URL, entry["upstream"])
	}
	if _, ok := entry["rt_ms"]; !ok {
		t.Errorf("expected rt_ms in filtered access log")
	}

	// 不包含未在白名单中的字段
	excludedFields := []string{"request_id", "client_ip", "error_kind", "bytes", "method"}
	for _, field := range excludedFields {
		if _, exists := entry[field]; exists {
			t.Errorf("field %s should be excluded by access_fields whitelist, but was found", field)
		}
	}
}

// TestAccessLogErrorKind 验证各类错误状态码下的 error_kind 推断及未匹配路由 404 access log
func TestAccessLogErrorKind(t *testing.T) {
	fakeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "504"):
			w.WriteHeader(http.StatusGatewayTimeout)
		case strings.HasSuffix(r.URL.Path, "500"):
			w.WriteHeader(http.StatusInternalServerError)
		case strings.HasSuffix(r.URL.Path, "503"):
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer fakeUpstream.Close()

	cfgTemplate := `
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
  output: %s
  max_size: 100
  max_age: 30
  max_backups: 10
  compress: false
  access: true
routes:
  - name: err-route
    match:
      path: /err
      type: prefix
    target:
      url: ` + fakeUpstream.URL + `
  - name: bad-gateway-route
    match:
      path: /bad-gateway
      type: prefix
    target:
      url: http://127.0.0.1:59999
`

	gwServer, logFile := setupGatewayTestEnv(t, cfgTemplate)
	defer gwServer.Close()

	client := &http.Client{Timeout: 3 * time.Second}

	cases := []struct {
		url               string
		expectedStatus    int
		expectedErrorKind string
	}{
		{url: gwServer.URL + "/err/504", expectedStatus: 504, expectedErrorKind: "timeout"},
		{url: gwServer.URL + "/err/500", expectedStatus: 500, expectedErrorKind: "upstream_error"},
		{url: gwServer.URL + "/err/503", expectedStatus: 503, expectedErrorKind: "upstream_error"},
		{url: gwServer.URL + "/bad-gateway/test", expectedStatus: 502, expectedErrorKind: "bad_gateway"},
		{url: gwServer.URL + "/not-found-route", expectedStatus: 404, expectedErrorKind: "not_found"},
	}

	for _, tc := range cases {
		resp, err := client.Get(tc.url)
		if err != nil {
			t.Fatalf("request to %s failed: %v", tc.url, err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode != tc.expectedStatus {
			t.Errorf("for %s: expected status %d, got %d", tc.url, tc.expectedStatus, resp.StatusCode)
		}
	}

	entries := readAccessLogEntries(t, logFile)
	if len(entries) < len(cases) {
		t.Fatalf("expected at least %d entries, got %d", len(cases), len(entries))
	}

	// 校验最后 len(cases) 条日志对应的 error_kind
	relevantEntries := entries[len(entries)-len(cases):]
	for i, tc := range cases {
		e := relevantEntries[i]
		if e["status"] != float64(tc.expectedStatus) {
			t.Errorf("case %d (%s): expected status %d, got %v", i, tc.url, tc.expectedStatus, e["status"])
		}
		if e["error_kind"] != tc.expectedErrorKind {
			t.Errorf("case %d (%s): expected error_kind %q, got %v", i, tc.url, tc.expectedErrorKind, e["error_kind"])
		}
	}
}

// TestAccessLogDecoupledFromDebug 验证 debug 级别下 access log 依然正常输出 (解耦验证)
func TestAccessLogDecoupledFromDebug(t *testing.T) {
	fakeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("debug_and_access"))
	}))
	defer fakeUpstream.Close()

	cfgTemplate := `
server:
  port: 8080
  mode: test
  read_timeout: "60s"
  write_timeout: "60s"
  max_header_bytes: 1048576
  graceful_shutdown_timeout: "30s"
log:
  level: debug
  format: json
  output: %s
  max_size: 100
  max_age: 30
  max_backups: 10
  compress: false
  access: true
routes:
  - name: debug-access-route
    match:
      path: /debug-access
      type: prefix
    target:
      url: ` + fakeUpstream.URL + `
`

	gwServer, logFile := setupGatewayTestEnv(t, cfgTemplate)
	defer gwServer.Close()

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(gwServer.URL + "/debug-access")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	_ = logger.Sync()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	content := string(data)

	// 1. 验证存在 debug 日志 (例如 "收到后端响应" 或 "返回给用户")
	if !strings.Contains(content, "收到后端响应") && !strings.Contains(content, "返回给用户") {
		t.Errorf("expected debug logs in output when level: debug, got: %s", content)
	}

	// 2. 验证 access log (msg: "access", level: "info") 依然存在
	entries := readAccessLogEntries(t, logFile)
	if len(entries) == 0 {
		t.Fatalf("expected access log entry even when level: debug (decoupled), got 0")
	}

	entry := entries[len(entries)-1]
	if entry["msg"] != "access" {
		t.Errorf("expected msg 'access', got %v", entry["msg"])
	}
	if entry["level"] != "info" {
		t.Errorf("expected level 'info', got %v", entry["level"])
	}
	if entry["status"] != float64(200) {
		t.Errorf("expected status 200, got %v", entry["status"])
	}
}
