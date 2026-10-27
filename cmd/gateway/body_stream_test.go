package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"gateway-go/internal/config"
	"gateway-go/internal/logger"
	"gateway-go/internal/plugin"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestBodyStreamIntegration 验证大报文流式转发与日志采样行为 (REQ-P0-01)
// 覆盖 debug 与 info 级别 x 4 种报文尺寸 (POST 8KB, POST 1MB, GET 100KB, GET 10MB)
func TestBodyStreamIntegration(t *testing.T) {
	var (
		lastUpstreamBody []byte
		lastUpstreamCL   int64
		upstreamMu       sync.Mutex
	)

	// 1. 创建假上游 HTTP 服务器
	fakeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamMu.Lock()
		defer upstreamMu.Unlock()

		lastUpstreamCL = r.ContentLength

		if r.Method == http.MethodPost {
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			lastUpstreamBody = bodyBytes
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}

		if r.Method == http.MethodGet {
			sizeStr := r.URL.Query().Get("size")
			size, _ := strconv.Atoi(sizeStr)
			if size <= 0 {
				size = 100
			}

			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.Itoa(size))
			w.WriteHeader(http.StatusOK)

			// 分块流式写出响应体，长度为 26 的整数倍以保证模式连续
			chunk := make([]byte, 26*1024)
			for i := range chunk {
				chunk[i] = byte('A' + (i % 26))
			}
			remaining := size
			for remaining > 0 {
				toWrite := len(chunk)
				if toWrite > remaining {
					toWrite = remaining
				}
				_, _ = w.Write(chunk[:toWrite])
				remaining -= toWrite
			}
			return
		}

		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer fakeUpstream.Close()

	// 2. 表驱动用例定义
	testCases := []struct {
		name        string
		logLevel    string
		method      string
		payloadSize int
	}{
		{name: "debug_post_8kb", logLevel: "debug", method: http.MethodPost, payloadSize: 8 * 1024},
		{name: "debug_post_1mb", logLevel: "debug", method: http.MethodPost, payloadSize: 1024 * 1024},
		{name: "debug_get_100kb", logLevel: "debug", method: http.MethodGet, payloadSize: 100 * 1024},
		{name: "debug_get_10mb", logLevel: "debug", method: http.MethodGet, payloadSize: 10 * 1024 * 1024},
		{name: "info_post_8kb", logLevel: "info", method: http.MethodPost, payloadSize: 8 * 1024},
		{name: "info_post_1mb", logLevel: "info", method: http.MethodPost, payloadSize: 1024 * 1024},
		{name: "info_get_100kb", logLevel: "info", method: http.MethodGet, payloadSize: 100 * 1024},
		{name: "info_get_10mb", logLevel: "info", method: http.MethodGet, payloadSize: 10 * 1024 * 1024},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			logFile := filepath.Join(tmpDir, "gateway.log")
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
  level: %s
  format: json
  output: %s
  max_size: 100
  max_age: 30
  max_backups: 10
  compress: false
  body_limit: 0
routes:
  - name: test-stream-route
    match:
      path: /test
      type: prefix
    target:
      url: %s
`, tc.logLevel, logFile, fakeUpstream.URL)

			if err := os.WriteFile(cfgFile, []byte(cfgContent), 0644); err != nil {
				t.Fatalf("failed to write test config: %v", err)
			}

			// 初始化全局配置与日志系统
			cm := config.NewConfigManager(cfgFile)
			if err := cm.LoadConfig(cfgFile); err != nil {
				t.Fatalf("failed to load config: %v", err)
			}
			configManager = cm

			if err := logger.Init(&cm.GetConfig().Log); err != nil {
				t.Fatalf("failed to init logger: %v", err)
			}

			// 初始化插件系统
			pluginManager = plugin.NewManager()
			registerPlugins()

			// 构建网关 HTTP 引擎并启动临时网关服务
			engine := buildEngine()
			gwServer := httptest.NewServer(engine)
			defer gwServer.Close()

			client := &http.Client{}

			if tc.method == http.MethodPost {
				// 生成确定性 payload
				payload := make([]byte, tc.payloadSize)
				for i := range payload {
					payload[i] = byte('0' + (i % 10))
				}
				expectedSum := sha256.Sum256(payload)
				expectedSHAHex := hex.EncodeToString(expectedSum[:])[:8]

				req, err := http.NewRequest(http.MethodPost, gwServer.URL+"/test", bytes.NewReader(payload))
				if err != nil {
					t.Fatalf("failed to create POST request: %v", err)
				}
				req.Header.Set("Content-Type", "application/octet-stream")
				req.ContentLength = int64(tc.payloadSize)

				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("POST request failed: %v", err)
				}
				defer resp.Body.Close()

				// 断言上游无 502 错误
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("expected status 200 OK, got %d", resp.StatusCode)
				}
				respBody, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("failed to read response body: %v", err)
				}
				if string(respBody) != "ok" {
					t.Fatalf("expected response body 'ok', got %q", string(respBody))
				}

				// 断言上游接收到完整的报文
				upstreamMu.Lock()
				receivedLen := len(lastUpstreamBody)
				contentLength := lastUpstreamCL
				receivedBytes := append([]byte(nil), lastUpstreamBody...)
				upstreamMu.Unlock()

				if contentLength != int64(tc.payloadSize) {
					t.Errorf("upstream Content-Length mismatch: expected %d, got %d", tc.payloadSize, contentLength)
				}
				if receivedLen != tc.payloadSize {
					t.Errorf("upstream received body length mismatch: expected %d, got %d", tc.payloadSize, receivedLen)
				}
				if !bytes.Equal(receivedBytes, payload) {
					t.Errorf("upstream received body content mismatch")
				}

				// 断言日志记录
				_ = logger.Sync()
				logData, err := os.ReadFile(logFile)
				if err != nil {
					t.Fatalf("failed to read log file: %v", err)
				}
				logStr := string(logData)

				// 1. 包含 body_bytes
				if !strings.Contains(logStr, "body_bytes") {
					t.Errorf("logs should contain 'body_bytes', got: %s", logStr)
				}
				bytesPattern := fmt.Sprintf(`"body_bytes":\s*%d`, tc.payloadSize)
				if matched, _ := regexp.MatchString(bytesPattern, logStr); !matched {
					t.Errorf("logs should contain %q, log output: %s", bytesPattern, logStr)
				}

				// 2. 包含 body_sha256 且为预期 8 位十六进制字符
				if !strings.Contains(logStr, "body_sha256") {
					t.Errorf("logs should contain 'body_sha256', got: %s", logStr)
				}
				shaPattern := fmt.Sprintf(`"body_sha256":\s*"%s"`, expectedSHAHex)
				if matched, _ := regexp.MatchString(shaPattern, logStr); !matched {
					t.Errorf("logs should contain expected sha %q, log output: %s", shaPattern, logStr)
				}
				hex8Pattern := `"body_sha256":\s*"[0-9a-f]{8}"`
				if matched, _ := regexp.MatchString(hex8Pattern, logStr); !matched {
					t.Errorf("body_sha256 must be 8 hex characters, pattern %s not matched in %s", hex8Pattern, logStr)
				}

				// 3. 当 body_limit == 0 时，不记录完整报文内容
				if strings.Contains(logStr, string(payload)) {
					t.Errorf("logs should NOT contain full body when body_limit == 0")
				}

			} else if tc.method == http.MethodGet {
				// 构造预期的 GET 响应报文内容与 SHA
				expectedPayload := make([]byte, tc.payloadSize)
				for i := range expectedPayload {
					expectedPayload[i] = byte('A' + (i % 26))
				}
				expectedSum := sha256.Sum256(expectedPayload)
				expectedSHAHex := hex.EncodeToString(expectedSum[:])[:8]

				reqURL := fmt.Sprintf("%s/test?size=%d", gwServer.URL, tc.payloadSize)
				resp, err := client.Get(reqURL)
				if err != nil {
					t.Fatalf("GET request failed: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					t.Fatalf("expected status 200 OK, got %d", resp.StatusCode)
				}

				// 客户端接收完整报文且无截断
				received, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("failed to read response body: %v", err)
				}
				if len(received) != tc.payloadSize {
					t.Fatalf("client received bytes mismatch: expected %d, got %d", tc.payloadSize, len(received))
				}
				if !bytes.Equal(received, expectedPayload) {
					t.Fatalf("client received content mismatch")
				}

				// 断言日志记录
				_ = logger.Sync()
				logData, err := os.ReadFile(logFile)
				if err != nil {
					t.Fatalf("failed to read log file: %v", err)
				}
				logStr := string(logData)

				// 1. 包含 body_bytes
				if !strings.Contains(logStr, "body_bytes") {
					t.Errorf("logs should contain 'body_bytes', got: %s", logStr)
				}
				bytesPattern := fmt.Sprintf(`"body_bytes":\s*%d`, tc.payloadSize)
				if matched, _ := regexp.MatchString(bytesPattern, logStr); !matched {
					t.Errorf("logs should contain %q, log output: %s", bytesPattern, logStr)
				}

				// 2. 包含 body_sha256 且为预期 8 位十六进制字符
				if !strings.Contains(logStr, "body_sha256") {
					t.Errorf("logs should contain 'body_sha256', got: %s", logStr)
				}
				shaPattern := fmt.Sprintf(`"body_sha256":\s*"%s"`, expectedSHAHex)
				if matched, _ := regexp.MatchString(shaPattern, logStr); !matched {
					t.Errorf("logs should contain expected sha %q, log output: %s", shaPattern, logStr)
				}
				hex8Pattern := `"body_sha256":\s*"[0-9a-f]{8}"`
				if matched, _ := regexp.MatchString(hex8Pattern, logStr); !matched {
					t.Errorf("body_sha256 must be 8 hex characters, pattern %s not matched in %s", hex8Pattern, logStr)
				}

				// 3. 当 body_limit == 0 时，不记录完整报文内容
				if strings.Contains(logStr, string(expectedPayload)) {
					t.Errorf("logs should NOT contain full body when body_limit == 0")
				}
			}
		})
	}
}

// TestBodyStreamWithBodyLimit 验证配置 body_limit > 0 时对报文进行采样而不影响流式转发完整性
func TestBodyStreamWithBodyLimit(t *testing.T) {
	fakeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Received-Bytes", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer fakeUpstream.Close()

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "gateway_limit.log")
	cfgFile := filepath.Join(tmpDir, "config.yaml")

	// 限制采样 512 字节
	bodyLimit := 512
	cfgContent := fmt.Sprintf(`
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
  body_limit: %d
routes:
  - name: test-sample-route
    match:
      path: /test
      type: prefix
    target:
      url: %s
`, logFile, bodyLimit, fakeUpstream.URL)

	if err := os.WriteFile(cfgFile, []byte(cfgContent), 0644); err != nil {
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
	defer gwServer.Close()

	// 发送 8KB 请求体
	payloadSize := 8 * 1024
	payload := bytes.Repeat([]byte("K"), payloadSize)

	req, _ := http.NewRequest(http.MethodPost, gwServer.URL+"/test", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(payloadSize)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// 确认上游依然收到完整的 8KB，没有被 512 字节截断
	recBytesHeader := resp.Header.Get("X-Received-Bytes")
	if recBytesHeader != strconv.Itoa(payloadSize) {
		t.Fatalf("upstream should receive full %d bytes, got %s", payloadSize, recBytesHeader)
	}

	_ = logger.Sync()
	logData, _ := os.ReadFile(logFile)
	logStr := string(logData)

	// 日志中记录了 body_bytes = 8192
	if !strings.Contains(logStr, fmt.Sprintf(`"body_bytes":%d`, payloadSize)) &&
		!strings.Contains(logStr, fmt.Sprintf(`"body_bytes": %d`, payloadSize)) {
		t.Errorf("log should contain body_bytes %d", payloadSize)
	}

	// 日志中的采样 body 字段包含恰好 512 个 'K'
	samplePattern := strings.Repeat("K", bodyLimit)
	if !strings.Contains(logStr, samplePattern) {
		t.Errorf("log should contain sampled 512 bytes")
	}

	// 日志中不应包含超过 512 字节的内容（例如 513 个 'K'）
	overSamplePattern := strings.Repeat("K", bodyLimit+1)
	if strings.Contains(logStr, overSamplePattern) {
		t.Errorf("log sampled body exceeded body_limit!")
	}
}

// TestBodyStreamSHA256Disabled 验证配置 body_sha256 = false 时关闭 SHA 计算
func TestBodyStreamSHA256Disabled(t *testing.T) {
	fakeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer fakeUpstream.Close()

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "gateway_nosha.log")
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
  level: debug
  format: json
  output: %s
  max_size: 100
  max_age: 30
  max_backups: 10
  compress: false
  body_limit: 0
  body_sha256: false
routes:
  - name: test-nosha-route
    match:
      path: /test
      type: prefix
    target:
      url: %s
`, logFile, fakeUpstream.URL)

	if err := os.WriteFile(cfgFile, []byte(cfgContent), 0644); err != nil {
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
	defer gwServer.Close()

	payload := bytes.Repeat([]byte("Z"), 8192)
	req, _ := http.NewRequest(http.MethodPost, gwServer.URL+"/test", bytes.NewReader(payload))
	req.ContentLength = int64(len(payload))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST request failed: %v", err)
	}
	defer resp.Body.Close()

	_ = logger.Sync()
	logData, _ := os.ReadFile(logFile)
	logStr := string(logData)

	// body_bytes 仍然记录
	if !strings.Contains(logStr, "body_bytes") {
		t.Errorf("log should contain body_bytes")
	}

	// body_sha256 不记录具体 hash
	hex8Pattern := `"body_sha256":\s*"[0-9a-f]{8}"`
	if matched, _ := regexp.MatchString(hex8Pattern, logStr); matched {
		t.Errorf("body_sha256 should not be computed when disabled: %s", logStr)
	}
}
