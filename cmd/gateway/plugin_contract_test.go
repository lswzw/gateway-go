package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"gateway-go/internal/config"
	"gateway-go/internal/plugin"
	"gateway-go/internal/plugin/core"
	"gateway-go/internal/plugin/plugins/circuitbreaker"
	"gateway-go/internal/plugin/plugins/consistency"
	"gateway-go/internal/plugin/plugins/cors"
	"gateway-go/internal/plugin/plugins/interface_auth"
	"gateway-go/internal/plugin/plugins/ipwhitelist"
	"gateway-go/internal/plugin/plugins/ratelimit"
	"gateway-go/internal/response"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// verifySingleValidJSON 验证响应体是且仅是一个合法的单个 JSON 对象，无任何拼接内容
func verifySingleValidJSON(t *testing.T, body []byte) response.ErrorResponse {
	t.Helper()
	var resp response.ErrorResponse
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("响应体不是合法的 JSON: %v, raw body: %s", err, string(body))
	}
	if dec.More() {
		t.Fatalf("响应体包含多个 JSON 对象或拼接数据: raw body: %s", string(body))
	}
	return resp
}

// TestContract_PluginWritesResponse 测试场景 1：插件写响应（如 ipwhitelist 403）
// 契约：插件写响应必须调用 c.Abort() 并返回 nil；响应体为单个合法 JSON，状态为 403，Content-Length 与实际一致
func TestContract_PluginWritesResponse(t *testing.T) {
	configManager = config.NewConfigManager("")
	pluginManager = plugin.NewManager()

	// 注册 ip_whitelist 插件
	ipPlugin := ipwhitelist.New()
	_ = ipPlugin.Init(map[string]interface{}{
		"ip_whitelist": []interface{}{"192.168.1.100"},
	})
	_ = pluginManager.Register(ipPlugin)
	_ = pluginManager.RegisterAvailablePlugin("ip_whitelist", ipPlugin)
	_ = pluginManager.LoadRoutePlugins("whitelist-route", []string{"ip_whitelist"})

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Name: "whitelist-route",
				Match: config.RouteMatch{
					Path: "/api/secure",
					Type: "exact",
				},
				Target: config.TargetConfig{
					URL: "internal://secure",
				},
				Plugins: []string{"ip_whitelist"},
			},
		},
	}
	configManager.SetConfig(cfg)

	engine := buildEngine()

	// 发起请求（默认客户端 IP 为 127.0.0.1 或 192.0.2.1，不在 192.168.1.100 白名单内）
	req, _ := http.NewRequest("GET", "/api/secure", nil)
	req.Header.Set("X-Request-Id", "req-whitelist-01")
	rec := httptest.NewRecorder()

	engine.ServeHTTP(rec, req)

	// 断言状态码为 403
	if rec.Code != http.StatusForbidden {
		t.Fatalf("期望状态码 403，实际获得 %d, body: %s", rec.Code, rec.Body.String())
	}

	// 断言 Content-Type
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("期望 Content-Type 为 application/json，实际获得 %s", ct)
	}

	// 断言 Content-Length 与实际 body 长度一致
	expectedCL := strconv.Itoa(rec.Body.Len())
	if cl := rec.Header().Get("Content-Length"); cl != expectedCL {
		t.Errorf("期望 Content-Length 为 %s，实际获得 %s", expectedCL, cl)
	}

	// 断言单个合法 JSON 且无二次拼接
	resp := verifySingleValidJSON(t, rec.Body.Bytes())
	if resp.Code != http.StatusForbidden {
		t.Errorf("期望 code 为 403，实际获得 %d", resp.Code)
	}
	if resp.Message != "IP 不在白名单中" {
		t.Errorf("期望 message 为 'IP 不在白名单中'，实际获得 %s", resp.Message)
	}
	if resp.RequestID != "req-whitelist-01" {
		t.Errorf("期望 request_id 为 'req-whitelist-01'，实际获得 %s", resp.RequestID)
	}
}

// mockErrorOnlyPlugin 仅返回错误，不写响应的插件
type mockErrorOnlyPlugin struct {
	*core.BasePlugin
	errToReturn error
}

func newMockErrorOnlyPlugin(name string, errToReturn error) *mockErrorOnlyPlugin {
	return &mockErrorOnlyPlugin{
		BasePlugin:  core.NewBasePlugin(name, 10, nil),
		errToReturn: errToReturn,
	}
}

func (p *mockErrorOnlyPlugin) Init(config interface{}) error { return nil }

func (p *mockErrorOnlyPlugin) Execute(ctx *gin.Context) error {
	return p.errToReturn
}

// TestContract_PluginReturnsErrorWithoutWriting 测试场景 2：插件返回 error 且未写响应
// 契约：网关核心兜底写 500，输出单个合法 JSON，包含 route、plugin、request_id
func TestContract_PluginReturnsErrorWithoutWriting(t *testing.T) {
	configManager = config.NewConfigManager("")
	pluginManager = plugin.NewManager()

	pluginErr := errors.New("database connection pool exhausted")
	p := newMockErrorOnlyPlugin("db_check", pluginErr)

	_ = pluginManager.Register(p)
	_ = pluginManager.RegisterAvailablePlugin("db_check", p)
	_ = pluginManager.LoadRoutePlugins("err-route", []string{"db_check"})

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Name: "err-route",
				Match: config.RouteMatch{
					Path: "/api/check",
					Type: "exact",
				},
				Target: config.TargetConfig{
					URL: "internal://check",
				},
				Plugins: []string{"db_check"},
			},
		},
	}
	configManager.SetConfig(cfg)

	engine := buildEngine()

	req, _ := http.NewRequest("GET", "/api/check", nil)
	req.Header.Set("X-Request-Id", "req-error-02")
	rec := httptest.NewRecorder()

	engine.ServeHTTP(rec, req)

	// 断言网关兜底返回 500
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("期望状态码 500，实际获得 %d, body: %s", rec.Code, rec.Body.String())
	}

	// 断言 Content-Type
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("期望 Content-Type 为 application/json，实际获得 %s", ct)
	}

	// 断言 Content-Length 与实际 body 长度一致
	expectedCL := strconv.Itoa(rec.Body.Len())
	if cl := rec.Header().Get("Content-Length"); cl != expectedCL {
		t.Errorf("期望 Content-Length 为 %s，实际获得 %s", expectedCL, cl)
	}

	// 断言单个合法 JSON
	resp := verifySingleValidJSON(t, rec.Body.Bytes())
	if resp.Code != http.StatusInternalServerError {
		t.Errorf("期望 code 为 500，实际获得 %d", resp.Code)
	}
	if resp.Message != "database connection pool exhausted" {
		t.Errorf("期望 message 包含错误信息，实际获得 %s", resp.Message)
	}
	if resp.RequestID != "req-error-02" {
		t.Errorf("期望 request_id 为 'req-error-02'，实际获得 %s", resp.RequestID)
	}
}

// mockRoguePlugin 既写响应又返回 error 的违规插件（用于防卫测试）
type mockRoguePlugin struct {
	*core.BasePlugin
}

func newMockRoguePlugin(name string) *mockRoguePlugin {
	return &mockRoguePlugin{
		BasePlugin: core.NewBasePlugin(name, 10, nil),
	}
}

func (p *mockRoguePlugin) Init(config interface{}) error { return nil }

func (p *mockRoguePlugin) Execute(ctx *gin.Context) error {
	// 违背契约：既通过统一出口写了 401 响应，又返回了 error
	response.RespondError(ctx, http.StatusUnauthorized, "token expired", response.GetRequestID(ctx))
	return errors.New("auth failed: token expired")
}

// TestContract_DefensivePluginWritesAndReturnsError 测试场景 3：防御性测试
// 当插件违规既写响应又返回 error 时，网关必须检查 c.Writer.Written()，严禁二次写 500，客户端获得插件原始响应且无双写
func TestContract_DefensivePluginWritesAndReturnsError(t *testing.T) {
	configManager = config.NewConfigManager("")
	pluginManager = plugin.NewManager()

	rogue := newMockRoguePlugin("rogue_auth")
	_ = pluginManager.Register(rogue)
	_ = pluginManager.RegisterAvailablePlugin("rogue_auth", rogue)
	_ = pluginManager.LoadRoutePlugins("rogue-route", []string{"rogue_auth"})

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Name: "rogue-route",
				Match: config.RouteMatch{
					Path: "/api/rogue",
					Type: "exact",
				},
				Target: config.TargetConfig{
					URL: "internal://rogue",
				},
				Plugins: []string{"rogue_auth"},
			},
		},
	}
	configManager.SetConfig(cfg)

	engine := buildEngine()

	req, _ := http.NewRequest("GET", "/api/rogue", nil)
	req.Header.Set("X-Request-Id", "req-rogue-03")
	rec := httptest.NewRecorder()

	engine.ServeHTTP(rec, req)

	// 断言收到的是插件写出的 401，而不是被网关二次改写或拼接的 500
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("期望状态码 401，实际获得 %d, body: %s", rec.Code, rec.Body.String())
	}

	// 断言 Content-Length 与实际 body 一致，不存在双写导致的长度不匹配或截断
	expectedCL := strconv.Itoa(rec.Body.Len())
	if cl := rec.Header().Get("Content-Length"); cl != expectedCL {
		t.Errorf("期望 Content-Length 为 %s，实际获得 %s", expectedCL, cl)
	}

	// 严格断言：body 必须是单个合法 JSON，不能存在 {"code":401...}{"code":500...} 双写
	resp := verifySingleValidJSON(t, rec.Body.Bytes())
	if resp.Code != http.StatusUnauthorized {
		t.Errorf("期望 code 为 401，实际获得 %d", resp.Code)
	}
	if resp.Message != "token expired" {
		t.Errorf("期望 message 为 'token expired'，实际获得 %s", resp.Message)
	}
	if resp.RequestID != "req-rogue-03" {
		t.Errorf("期望 request_id 为 'req-rogue-03'，实际获得 %s", resp.RequestID)
	}
}

// TestContract_RateLimitPlugin 测试内置插件 ratelimit 契约（写 429 且返回 nil）
func TestContract_RateLimitPlugin(t *testing.T) {
	configManager = config.NewConfigManager("")
	pluginManager = plugin.NewManager()

	rlPlugin := ratelimit.New()
	_ = rlPlugin.Init(map[string]interface{}{
		"requests_per_second": float64(0.0001),
		"burst":               0, // 初始令牌数为 0，必定触发限流
	})
	_ = pluginManager.Register(rlPlugin)
	_ = pluginManager.RegisterAvailablePlugin("rate_limit", rlPlugin)
	_ = pluginManager.LoadRoutePlugins("ratelimit-route", []string{"rate_limit"})

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Name: "ratelimit-route",
				Match: config.RouteMatch{
					Path: "/api/limited",
					Type: "exact",
				},
				Target: config.TargetConfig{
					URL: "internal://limited",
				},
				Plugins: []string{"rate_limit"},
			},
		},
	}
	configManager.SetConfig(cfg)

	engine := buildEngine()

	req, _ := http.NewRequest("GET", "/api/limited", nil)
	req.Header.Set("X-Request-Id", "req-ratelimit-04")
	rec := httptest.NewRecorder()

	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("期望状态码 429，实际获得 %d, body: %s", rec.Code, rec.Body.String())
	}

	expectedCL := strconv.Itoa(rec.Body.Len())
	if cl := rec.Header().Get("Content-Length"); cl != expectedCL {
		t.Errorf("期望 Content-Length 为 %s，实际获得 %s", expectedCL, cl)
	}

	resp := verifySingleValidJSON(t, rec.Body.Bytes())
	if resp.Code != http.StatusTooManyRequests {
		t.Errorf("期望 code 为 429，实际获得 %d", resp.Code)
	}
	if resp.Message != "请求过于频繁" {
		t.Errorf("期望 message 为 '请求过于频繁'，实际获得 %s", resp.Message)
	}
	if resp.RequestID != "req-ratelimit-04" {
		t.Errorf("期望 request_id 为 'req-ratelimit-04'，实际获得 %s", resp.RequestID)
	}
}

// TestContract_CircuitBreakerPlugin 测试内置插件 circuitbreaker 契约（写 503 且返回 nil）
func TestContract_CircuitBreakerPlugin(t *testing.T) {
	configManager = config.NewConfigManager("")
	pluginManager = plugin.NewManager()

	cbPlugin := circuitbreaker.New()
	_ = cbPlugin.Init(map[string]interface{}{
		"failure_threshold": int64(1),
		"open_timeout":      int64(60),
	})
	_ = pluginManager.Register(cbPlugin)
	_ = pluginManager.RegisterAvailablePlugin("circuit_breaker", cbPlugin)
	_ = pluginManager.LoadRoutePlugins("cb-route", []string{"circuit_breaker"})

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Name: "cb-route",
				Match: config.RouteMatch{
					Path: "/api/circuit",
					Type: "exact",
				},
				Target: config.TargetConfig{
					URL: "internal://circuit",
				},
				Plugins: []string{"circuit_breaker"},
			},
		},
	}
	configManager.SetConfig(cfg)

	// 模拟触发熔断：让底层熔断器状态变为 StateOpen
	// 路由匹配时 c.Set("target", matchedRoute.Target.URL)，此处 target 为 "internal://circuit"
	cb := cbPlugin.GetCircuitBreaker("internal://circuit")
	cb.SetState(circuitbreaker.StateOpen)

	engine := buildEngine()

	req, _ := http.NewRequest("GET", "/api/circuit", nil)
	req.Header.Set("X-Request-Id", "req-circuit-05")
	rec := httptest.NewRecorder()

	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("期望状态码 503，实际获得 %d, body: %s", rec.Code, rec.Body.String())
	}

	expectedCL := strconv.Itoa(rec.Body.Len())
	if cl := rec.Header().Get("Content-Length"); cl != expectedCL {
		t.Errorf("期望 Content-Length 为 %s，实际获得 %s", expectedCL, cl)
	}

	resp := verifySingleValidJSON(t, rec.Body.Bytes())
	if resp.Code != http.StatusServiceUnavailable {
		t.Errorf("期望 code 为 503，实际获得 %d", resp.Code)
	}
	if resp.Message != "服务暂时不可用" {
		t.Errorf("期望 message 为 '服务暂时不可用'，实际获得 %s", resp.Message)
	}
}

// TestContract_InterfaceAuthPlugin 测试内置插件 interface_auth 契约（无 token 时写 401 且返回 nil）
func TestContract_InterfaceAuthPlugin(t *testing.T) {
	configManager = config.NewConfigManager("")
	pluginManager = plugin.NewManager()

	authPlugin := interface_auth.New()
	_ = authPlugin.Init(map[string]interface{}{
		"white_interfaces": []string{"/public"},
	})
	_ = pluginManager.Register(authPlugin)
	_ = pluginManager.RegisterAvailablePlugin("interface_auth", authPlugin)
	_ = pluginManager.LoadRoutePlugins("auth-route", []string{"interface_auth"})

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Name: "auth-route",
				Match: config.RouteMatch{
					Path: "/api/protected",
					Type: "exact",
				},
				Target: config.TargetConfig{
					URL: "internal://protected",
				},
				Plugins: []string{"interface_auth"},
			},
		},
	}
	configManager.SetConfig(cfg)

	engine := buildEngine()

	req, _ := http.NewRequest("GET", "/api/protected", nil)
	req.Header.Set("X-Request-Id", "req-auth-06")
	rec := httptest.NewRecorder()

	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("期望状态码 401，实际获得 %d, body: %s", rec.Code, rec.Body.String())
	}

	expectedCL := strconv.Itoa(rec.Body.Len())
	if cl := rec.Header().Get("Content-Length"); cl != expectedCL {
		t.Errorf("期望 Content-Length 为 %s，实际获得 %s", expectedCL, cl)
	}

	resp := verifySingleValidJSON(t, rec.Body.Bytes())
	if resp.Code != http.StatusUnauthorized {
		t.Errorf("期望 code 为 401，实际获得 %d", resp.Code)
	}
	if resp.Message != "Token missing or invalid" {
		t.Errorf("期望 message 为 'Token missing or invalid'，实际获得 %s", resp.Message)
	}
}

// TestContract_ConsistencyPlugin 测试内置插件 consistency 契约（验签失败写 400 且返回 nil）
func TestContract_ConsistencyPlugin(t *testing.T) {
	configManager = config.NewConfigManager("")
	pluginManager = plugin.NewManager()

	consPlugin := consistency.New()
	_ = consPlugin.Init(map[string]interface{}{
		"enabled":         true,
		"signature_field": "X-Signature",
	})
	_ = pluginManager.Register(consPlugin)
	_ = pluginManager.RegisterAvailablePlugin("consistency", consPlugin)
	_ = pluginManager.LoadRoutePlugins("cons-route", []string{"consistency"})

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Name: "cons-route",
				Match: config.RouteMatch{
					Path: "/api/consistent",
					Type: "exact",
				},
				Target: config.TargetConfig{
					URL: "internal://consistent",
				},
				Plugins: []string{"consistency"},
			},
		},
	}
	configManager.SetConfig(cfg)

	engine := buildEngine()

	// 请求未携带 X-Signature 头
	req, _ := http.NewRequest("GET", "/api/consistent", nil)
	req.Header.Set("X-Request-Id", "req-cons-07")
	rec := httptest.NewRecorder()

	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("期望状态码 400，实际获得 %d, body: %s", rec.Code, rec.Body.String())
	}

	expectedCL := strconv.Itoa(rec.Body.Len())
	if cl := rec.Header().Get("Content-Length"); cl != expectedCL {
		t.Errorf("期望 Content-Length 为 %s，实际获得 %s", expectedCL, cl)
	}

	resp := verifySingleValidJSON(t, rec.Body.Bytes())
	if resp.Code != http.StatusBadRequest {
		t.Errorf("期望 code 为 400，实际获得 %d", resp.Code)
	}
	if resp.Message != "missing signature" {
		t.Errorf("期望 message 为 'missing signature'，实际获得 %s", resp.Message)
	}
}

// TestContract_CORSPlugin 测试内置插件 cors 契约（预检被拒时写 403 统一错误并返回 nil）
func TestContract_CORSPlugin(t *testing.T) {
	configManager = config.NewConfigManager("")
	pluginManager = plugin.NewManager()

	corsPlugin := cors.New()
	_ = corsPlugin.Init(map[string]interface{}{
		"allowed_origins": []interface{}{"https://allowed.example.com"},
	})
	_ = pluginManager.Register(corsPlugin)
	_ = pluginManager.RegisterAvailablePlugin("cors", corsPlugin)
	_ = pluginManager.LoadRoutePlugins("cors-route", []string{"cors"})

	cfg := &config.Config{
		Routes: []config.RouteConfig{
			{
				Name: "cors-route",
				Match: config.RouteMatch{
					Path: "/api/cors",
					Type: "exact",
				},
				Target: config.TargetConfig{
					URL: "internal://cors",
				},
				Plugins: []string{"cors"},
			},
		},
	}
	configManager.SetConfig(cfg)

	engine := buildEngine()

	// OPTIONS 预检请求携带未允许的 Origin
	req, _ := http.NewRequest("OPTIONS", "/api/cors", nil)
	req.Header.Set("Origin", "https://forbidden.example.com")
	req.Header.Set("X-Request-Id", "req-cors-08")
	rec := httptest.NewRecorder()

	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("期望状态码 403，实际获得 %d, body: %s", rec.Code, rec.Body.String())
	}

	expectedCL := strconv.Itoa(rec.Body.Len())
	if cl := rec.Header().Get("Content-Length"); cl != expectedCL {
		t.Errorf("期望 Content-Length 为 %s，实际获得 %s", expectedCL, cl)
	}

	resp := verifySingleValidJSON(t, rec.Body.Bytes())
	if resp.Code != http.StatusForbidden {
		t.Errorf("期望 code 为 403，实际获得 %d", resp.Code)
	}
	if resp.Message != "CORS origin not allowed" {
		t.Errorf("期望 message 为 'CORS origin not allowed'，实际获得 %s", resp.Message)
	}
}
