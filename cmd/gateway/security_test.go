package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gateway-go/internal/config"
	"gateway-go/internal/plugin"
	"gateway-go/internal/response"
)

// TestMaxBodyBytes413 验证当请求体超过 max_body_bytes 时返回 HTTP 413 Payload Too Large 及统一 JSON 格式 (REQ-P1-05)
func TestMaxBodyBytes413(t *testing.T) {
	// 创建后端测试上游
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fmt.Sprintf("ok: %d bytes", len(body))))
	}))
	defer upstream.Close()

	t.Run("ExceedsCustomLimit_Returns413UnifiedJSON", func(t *testing.T) {
		configManager = config.NewConfigManager("")
		pluginManager = plugin.NewManager()

		var maxBytes int64 = 512 // 限制 512 字节
		cfg := &config.Config{
			Server: config.ServerConfig{
				MaxBodyBytes: maxBytes,
			},
			Routes: []config.RouteConfig{
				{
					Name: "echo-route",
					Match: config.RouteMatch{
						Path: "/echo",
						Type: "exact",
					},
					Target: config.TargetConfig{
						URL: upstream.URL,
					},
				},
			},
		}
		configManager.SetConfig(cfg)
		engine := buildEngine()
		gwServer := httptest.NewServer(engine)
		defer gwServer.Close()

		client := gwServer.Client()

		// 1. 发送 1024 字节数据（超出 512 字节限制），应被拦截并返回 413
		largePayload := strings.Repeat("A", 1024)
		req, _ := http.NewRequest("POST", gwServer.URL+"/echo", strings.NewReader(largePayload))
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("X-Request-Id", "req-body-limit-01")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("client.Do failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413 Payload Too Large, got %d", resp.StatusCode)
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		var errResp response.ErrorResponse
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			t.Fatalf("response is not valid JSON: %v, raw: %s", err, string(bodyBytes))
		}

		if errResp.Code != 413 {
			t.Errorf("expected resp.code = 413, got %d", errResp.Code)
		}
		if errResp.Message != "请求体超过限制大小" {
			t.Errorf("expected resp.message = '请求体超过限制大小', got '%s'", errResp.Message)
		}
		if errResp.RequestID != "req-body-limit-01" {
			t.Errorf("expected resp.request_id = 'req-body-limit-01', got '%s'", errResp.RequestID)
		}

		// 2. 发送 256 字节数据（在 512 字节限制内），应成功通过
		smallPayload := strings.Repeat("B", 256)
		reqOK, _ := http.NewRequest("POST", gwServer.URL+"/echo", strings.NewReader(smallPayload))
		reqOK.Header.Set("Content-Type", "text/plain")
		reqOK.Header.Set("X-Request-Id", "req-body-limit-02")

		respOK, err := client.Do(reqOK)
		if err != nil {
			t.Fatalf("client.Do failed: %v", err)
		}
		defer respOK.Body.Close()

		if respOK.StatusCode != http.StatusOK {
			bodyOK, _ := io.ReadAll(respOK.Body)
			t.Fatalf("expected 200 for payload within limit, got %d, body: %s", respOK.StatusCode, string(bodyOK))
		}

		bodyOK, _ := io.ReadAll(respOK.Body)
		if !strings.Contains(string(bodyOK), "ok: 256 bytes") {
			t.Errorf("expected response to contain 'ok: 256 bytes', got %s", string(bodyOK))
		}
	})

	t.Run("DefaultLimit1MB_Exceeded_Returns413", func(t *testing.T) {
		configManager = config.NewConfigManager("")
		pluginManager = plugin.NewManager()

		// 未显式配置 MaxBodyBytes，应默认 1MB (1048576 字节)
		cfg := &config.Config{
			Server: config.ServerConfig{
				MaxBodyBytes: 0, // 触发默认 1MB
			},
			Routes: []config.RouteConfig{
				{
					Name: "default-limit-route",
					Match: config.RouteMatch{
						Path: "/default-limit",
						Type: "exact",
					},
					Target: config.TargetConfig{
						URL: upstream.URL,
					},
				},
			},
		}
		configManager.SetConfig(cfg)
		engine := buildEngine()
		gwServer := httptest.NewServer(engine)
		defer gwServer.Close()

		client := gwServer.Client()

		// 构造 1MB + 1 字节数据 (1048577 字节)
		overPayload := bytes.Repeat([]byte("X"), 1048576+1)
		req, _ := http.NewRequest("POST", gwServer.URL+"/default-limit", bytes.NewReader(overPayload))
		req.Header.Set("X-Request-Id", "req-default-1mb-01")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("client.Do failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413 for payload > 1MB, got %d", resp.StatusCode)
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		var errResp response.ErrorResponse
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			t.Fatalf("response is not valid JSON: %v, raw: %s", err, string(bodyBytes))
		}
		if errResp.Code != 413 || errResp.Message != "请求体超过限制大小" {
			t.Errorf("unexpected response content: %+v", errResp)
		}
	})

	t.Run("ChunkedStreamWithoutContentLength_Exceeded_Returns413", func(t *testing.T) {
		configManager = config.NewConfigManager("")
		pluginManager = plugin.NewManager()

		var maxBytes int64 = 100 // 限制 100 字节
		cfg := &config.Config{
			Server: config.ServerConfig{
				MaxBodyBytes: maxBytes,
			},
			Routes: []config.RouteConfig{
				{
					Name: "chunked-route",
					Match: config.RouteMatch{
						Path: "/chunked",
						Type: "exact",
					},
					Target: config.TargetConfig{
						URL: upstream.URL,
					},
				},
			},
		}
		configManager.SetConfig(cfg)
		engine := buildEngine()
		gwServer := httptest.NewServer(engine)
		defer gwServer.Close()

		client := gwServer.Client()

		// 模拟没有 Content-Length (ContentLength = -1) 的流式分块请求
		streamPayload := strings.Repeat("C", 300)
		req, _ := http.NewRequest("POST", gwServer.URL+"/chunked", io.NopCloser(strings.NewReader(streamPayload)))
		req.ContentLength = -1
		req.Header.Set("X-Request-Id", "req-chunked-01")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("client.Do failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 413 for chunked payload exceeding limit, got %d, body: %s", resp.StatusCode, string(body))
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		var errResp response.ErrorResponse
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			t.Fatalf("response is not valid JSON: %v, raw: %s", err, string(bodyBytes))
		}
		if errResp.Code != 413 || errResp.Message != "请求体超过限制大小" {
			t.Errorf("unexpected response content: %+v", errResp)
		}
	})
}
