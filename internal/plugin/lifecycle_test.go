package plugin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"gateway-go/internal/plugin/core"
	"gateway-go/internal/plugin/plugins/circuitbreaker"
	"gateway-go/internal/plugin/plugins/consistency"
	"gateway-go/internal/plugin/plugins/cors"
	"gateway-go/internal/plugin/plugins/interface_auth"
	"gateway-go/internal/plugin/plugins/ipwhitelist"
	"gateway-go/internal/plugin/plugins/ratelimit"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestPluginInitIdempotent 验证重复调用 Init 不会泄漏后台协程 (REQ-P1-02)
func TestPluginInitIdempotent(t *testing.T) {
	// 1. 测试 RateLimitPlugin 重复 Init
	t.Run("RateLimitPlugin_Init_Idempotent", func(t *testing.T) {
		rl := ratelimit.New()
		cfg := map[string]interface{}{
			"requests_per_second": 100.0,
			"burst":               100,
			"max_entries":         1000,
		}

		// 预热并获取初始协程基线
		if err := rl.Init(cfg); err != nil {
			t.Fatalf("初始 Init 失败: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
		runtime.GC()
		baseGoroutines := runtime.NumGoroutine()

		// 重复调用 Init 50 次
		for i := 0; i < 50; i++ {
			if err := rl.Init(cfg); err != nil {
				t.Fatalf("第 %d 次 Init 失败: %v", i, err)
			}
		}

		time.Sleep(100 * time.Millisecond)
		runtime.GC()
		afterGoroutines := runtime.NumGoroutine()

		// 容差范围应极小（不允许增长 ~50 个协程）
		if afterGoroutines > baseGoroutines+3 {
			t.Fatalf("RateLimitPlugin 重复 Init 发生协程泄漏: 初始 %d, 重复50次后 %d", baseGoroutines, afterGoroutines)
		}

		_ = rl.Stop()
	})

	// 2. 测试 CircuitBreakerPlugin 重复 Init
	t.Run("CircuitBreakerPlugin_Init_Idempotent", func(t *testing.T) {
		cb := circuitbreaker.New()
		cfg := map[string]interface{}{
			"failure_threshold": 50,
			"recovery_timeout":  10,
			"max_entries":       500,
		}

		if err := cb.Init(cfg); err != nil {
			t.Fatalf("初始 Init 失败: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
		runtime.GC()
		baseGoroutines := runtime.NumGoroutine()

		// 重复调用 Init 50 次
		for i := 0; i < 50; i++ {
			if err := cb.Init(cfg); err != nil {
				t.Fatalf("第 %d 次 Init 失败: %v", i, err)
			}
		}

		time.Sleep(100 * time.Millisecond)
		runtime.GC()
		afterGoroutines := runtime.NumGoroutine()

		if afterGoroutines > baseGoroutines+3 {
			t.Fatalf("CircuitBreakerPlugin 重复 Init 发生协程泄漏: 初始 %d, 重复50次后 %d", baseGoroutines, afterGoroutines)
		}

		_ = cb.Stop()
	})

	// 3. 测试 ConsistencyPlugin 重复 Init
	t.Run("ConsistencyPlugin_Init_Idempotent", func(t *testing.T) {
		cp := consistency.New()
		cfg := map[string]interface{}{
			"enabled":            true,
			"algorithm":          "hmac-sha256",
			"secret":             "my-secret",
			"timestamp_validity": int64(300),
		}

		for i := 0; i < 50; i++ {
			if err := cp.Init(cfg); err != nil {
				t.Fatalf("第 %d 次 Init 失败: %v", i, err)
			}
		}
		_ = cp.Stop()
	})

	// 4. 测试 Manager.LoadAvailablePlugins 连续重载 20 次协程稳定
	t.Run("Manager_LoadAvailablePlugins_Idempotent", func(t *testing.T) {
		mgr := NewManager()
		_ = mgr.Register(ratelimit.New())
		_ = mgr.Register(circuitbreaker.New())
		_ = mgr.Register(cors.New())

		configs := []PluginConfig{
			{Name: "rate_limit", Enabled: true, Order: 10, Config: map[string]interface{}{"requests_per_second": 50.0, "burst": 50}},
			{Name: "circuit_breaker", Enabled: true, Order: 20, Config: map[string]interface{}{"failure_threshold": 30}},
			{Name: "cors", Enabled: true, Order: 30, Config: map[string]interface{}{"allowed_origins": []interface{}{"*"}}},
		}

		if err := mgr.LoadAvailablePlugins(configs); err != nil {
			t.Fatalf("初始加载插件失败: %v", err)
		}

		time.Sleep(50 * time.Millisecond)
		runtime.GC()
		baseGoroutines := runtime.NumGoroutine()

		for i := 0; i < 20; i++ {
			if err := mgr.LoadAvailablePlugins(configs); err != nil {
				t.Fatalf("第 %d 次加载插件失败: %v", i, err)
			}
		}

		time.Sleep(100 * time.Millisecond)
		runtime.GC()
		afterGoroutines := runtime.NumGoroutine()

		if afterGoroutines > baseGoroutines+3 {
			t.Fatalf("Manager.LoadAvailablePlugins 重复调用发生协程泄漏: 初始 %d, 20次后 %d", baseGoroutines, afterGoroutines)
		}

		_ = mgr.StopAll()
	})
}

// TestPluginStopTwice 验证所有插件与管理器多次调用 Stop 不 panic 且幂等 (REQ-P1-02)
func TestPluginStopTwice(t *testing.T) {
	plugins := []struct {
		name   string
		plugin core.Plugin
		cfg    interface{}
	}{
		{
			name:   "ratelimit",
			plugin: ratelimit.New(),
			cfg:    map[string]interface{}{"requests_per_second": 10.0, "burst": 20},
		},
		{
			name:   "circuitbreaker",
			plugin: circuitbreaker.New(),
			cfg:    map[string]interface{}{"failure_threshold": 50},
		},
		{
			name:   "consistency",
			plugin: consistency.New(),
			cfg:    map[string]interface{}{"enabled": true, "secret": "sec"},
		},
		{
			name:   "cors",
			plugin: cors.New(),
			cfg:    map[string]interface{}{"allowed_origins": []interface{}{"*"}},
		},
		{
			name:   "ipwhitelist",
			plugin: ipwhitelist.New(),
			cfg:    map[string]interface{}{"ip_whitelist": []interface{}{"127.0.0.1"}},
		},
		{
			name:   "interface_auth",
			plugin: interface_auth.New(),
			cfg: map[string]interface{}{
				"white_interfaces": []interface{}{"/health"},
				"consumers": map[string]interface{}{
					"host":     "127.0.0.1:8080",
					"auth_api": "/auth",
				},
			},
		},
	}

	for _, tc := range plugins {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.plugin.Init(tc.cfg); err != nil {
				t.Fatalf("%s Init 失败: %v", tc.name, err)
			}

			// 第 1 次 Stop
			if err := tc.plugin.Stop(); err != nil {
				t.Errorf("%s 第一次 Stop 失败: %v", tc.name, err)
			}

			// 第 2 次 Stop (必须安全不 panic)
			if err := tc.plugin.Stop(); err != nil {
				t.Errorf("%s 第二次 Stop 失败: %v", tc.name, err)
			}

			// 第 3 次 Stop
			if err := tc.plugin.Stop(); err != nil {
				t.Errorf("%s 第三次 Stop 失败: %v", tc.name, err)
			}
		})
	}

	// 测试 Manager.StopAll 幂等
	t.Run("Manager_StopAll_Twice", func(t *testing.T) {
		mgr := NewManager()
		_ = mgr.Register(ratelimit.New())
		_ = mgr.LoadAvailablePlugins([]PluginConfig{
			{Name: "rate_limit", Enabled: true, Order: 1, Config: map[string]interface{}{"burst": 10}},
		})

		if err := mgr.StopAll(); err != nil {
			t.Fatalf("第一次 StopAll 失败: %v", err)
		}
		if err := mgr.StopAll(); err != nil {
			t.Fatalf("第二次 StopAll 失败: %v", err)
		}
	})

	// 测试 LifecycleManager.Close 幂等
	t.Run("LifecycleManager_Close_Twice", func(t *testing.T) {
		lm := NewLifecycleManager()
		lm.Close()
		lm.Close() // 不 panic
	})

	// 测试 PluginCache.Stop 幂等
	t.Run("PluginCache_Stop_Twice", func(t *testing.T) {
		pc := NewPluginCache(5 * time.Second)
		pc.Stop()
		pc.Stop() // 不 panic
	})
}

// TestRateLimitBucketCap 验证限流桶容量上限与 LRU 淘汰保障内存有界 (REQ-P1-02)
// 验收标准：发送 50,000 个不同 IP 的请求，桶数量严格受 max_entries 约束，内存稳定
func TestRateLimitBucketCap(t *testing.T) {
	t.Run("50000_Requests_Respect_MaxEntries", func(t *testing.T) {
		rl := ratelimit.New()
		const capLimit = 1000
		cfg := map[string]interface{}{
			"ip_based":            true,
			"requests_per_second": 1000.0,
			"burst":               1000,
			"max_entries":         capLimit,
		}
		if err := rl.Init(cfg); err != nil {
			t.Fatalf("初始化限流插件失败: %v", err)
		}
		defer rl.Stop()

		if rl.MaxEntries() != capLimit {
			t.Fatalf("预期 max_entries 为 %d，实际为 %d", capLimit, rl.MaxEntries())
		}

		// 发送 50,000 个不同客户端 IP 的请求
		const totalRequests = 50000
		for i := 0; i < totalRequests; i++ {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req, _ := http.NewRequest("GET", "/api/data", nil)
			// 生成不同 IP: 10.x.x.x
			ip := fmt.Sprintf("10.%d.%d.%d", (i>>16)&0xFF, (i>>8)&0xFF, i&0xFF)
			req.RemoteAddr = ip + ":12345"
			c.Request = req

			_ = rl.Execute(c)
		}

		// 校验当前桶总数绝不超过 max_entries
		bucketCount := rl.BucketCount()
		if bucketCount > capLimit {
			t.Fatalf("桶数量超过上限: 期望 <= %d, 实际 %d", capLimit, bucketCount)
		}
		if bucketCount != capLimit {
			t.Fatalf("发送 50,000 个不同IP后，桶数量应打满容量上限: 期望 %d, 实际 %d", capLimit, bucketCount)
		}
	})

	t.Run("LRU_Eviction_Order", func(t *testing.T) {
		// 容量设为 2，依次访问 IP-A, IP-B, IP-A, IP-C
		// 期望：IP-B 最久未访问被淘汰，IP-A 与 IP-C 保留
		rl := ratelimit.New()
		cfg := map[string]interface{}{
			"ip_based":            true,
			"requests_per_second": 1.0,
			"burst":               1,
			"max_entries":         2,
		}
		if err := rl.Init(cfg); err != nil {
			t.Fatalf("初始化失败: %v", err)
		}
		defer rl.Stop()

		sendReq := func(ip string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req, _ := http.NewRequest("GET", "/test", nil)
			req.RemoteAddr = ip + ":8080"
			c.Request = req
			_ = rl.Execute(c)
			return w
		}

		// 1. 请求 IP-A (消耗 A 的令牌)
		wA1 := sendReq("1.1.1.1")
		if wA1.Code == http.StatusTooManyRequests {
			t.Fatalf("IP-A 首次请求不应被限流")
		}

		// 2. 请求 IP-B (消耗 B 的令牌)
		wB1 := sendReq("2.2.2.2")
		if wB1.Code == http.StatusTooManyRequests {
			t.Fatalf("IP-B 首次请求不应被限流")
		}

		// 此时容量已满 (A, B)，再次访问 IP-A，使其成为最近使用 (MRU)，B 成为 LRU
		wA2 := sendReq("1.1.1.1")
		if wA2.Code != http.StatusTooManyRequests {
			t.Fatalf("IP-A 令牌已耗尽，第二次应被限流 (429)")
		}

		// 3. 请求新 IP-C (插入 C，应淘汰 LRU 即 IP-B)
		wC1 := sendReq("3.3.3.3")
		if wC1.Code == http.StatusTooManyRequests {
			t.Fatalf("IP-C 首次请求不应被限流")
		}

		// 此时桶内应为 A 和 C，B 已被淘汰！
		if rl.BucketCount() != 2 {
			t.Fatalf("期望桶数量为 2, 实际为 %d", rl.BucketCount())
		}

		// IP-A 仍在桶内（令牌仍耗尽，应依然返回 429）
		wA3 := sendReq("1.1.1.1")
		if wA3.Code != http.StatusTooManyRequests {
			t.Fatalf("IP-A 应仍在缓存中被限流")
		}

		// IP-B 之前被淘汰，重新访问等同于新 IP 重新创建桶（获得新初始令牌），应不被限流！
		wB2 := sendReq("2.2.2.2")
		if wB2.Code == http.StatusTooManyRequests {
			t.Fatalf("IP-B 应已被 LRU 淘汰，重建后应放行")
		}
	})
}

// TestCircuitBreakerCap 验证熔断器实例容量上限与 LRU 淘汰 (REQ-P1-02)
func TestCircuitBreakerCap(t *testing.T) {
	cb := circuitbreaker.New()
	const capLimit = 10
	cfg := map[string]interface{}{
		"failure_threshold": 50,
		"recovery_timeout":  10,
		"max_entries":       capLimit,
	}
	if err := cb.Init(cfg); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	defer cb.Stop()

	// 访问 100 个不同 target
	for i := 0; i < 100; i++ {
		target := fmt.Sprintf("service-%d", i)
		_ = cb.GetCircuitBreaker(target)
	}

	if cb.BreakerCount() > capLimit {
		t.Fatalf("熔断器实例超过上限: 期望 <= %d, 实际 %d", capLimit, cb.BreakerCount())
	}
	if cb.BreakerCount() != capLimit {
		t.Fatalf("期望打满熔断器容量 %d, 实际 %d", capLimit, cb.BreakerCount())
	}
}

// TestPluginCacheCap 验证插件结果缓存容量上限与 LRU 淘汰 (REQ-P1-02)
func TestPluginCacheCap(t *testing.T) {
	const capLimit = 5
	cache := NewPluginCacheWithCapacity(10*time.Second, capLimit)
	defer cache.Stop()

	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("key-%d", i)
		cache.Set(key, map[string]interface{}{"value": i})
	}

	if cache.Len() > capLimit {
		t.Fatalf("缓存大小超过上限: 期望 <= %d, 实际 %d", capLimit, cache.Len())
	}
	if cache.Len() != capLimit {
		t.Fatalf("期望打满缓存上限 %d, 实际 %d", capLimit, cache.Len())
	}
}

// TestManagerRemovedPluginStop 验证 Manager 重新加载配置时摘除可用插件并执行 Stop (REQ-P1-02)
func TestManagerRemovedPluginStop(t *testing.T) {
	mgr := NewManager()

	rl := ratelimit.New()
	cb := circuitbreaker.New()
	cs := cors.New()

	_ = mgr.Register(rl)
	_ = mgr.Register(cb)
	_ = mgr.Register(cs)

	// 初始加载 3 个插件
	initialConfigs := []PluginConfig{
		{Name: "rate_limit", Enabled: true, Order: 1, Config: map[string]interface{}{"burst": 10}},
		{Name: "circuit_breaker", Enabled: true, Order: 2, Config: map[string]interface{}{"failure_threshold": 50}},
		{Name: "cors", Enabled: true, Order: 3, Config: map[string]interface{}{"allowed_origins": []interface{}{"*"}}},
	}
	if err := mgr.LoadAvailablePlugins(initialConfigs); err != nil {
		t.Fatalf("初始加载失败: %v", err)
	}

	if _, ok := mgr.GetAvailablePlugin("rate_limit"); !ok {
		t.Fatalf("rate_limit 应在可用列表中")
	}

	// 路由配置引用 rate_limit，此时应成功
	if err := mgr.LoadRoutePlugins("route-1", []string{"rate_limit"}); err != nil {
		t.Fatalf("路由加载插件失败: %v", err)
	}

	// 重新加载配置：移除 rate_limit (只保留 circuit_breaker 与 cors)
	newConfigs := []PluginConfig{
		{Name: "circuit_breaker", Enabled: true, Order: 1, Config: map[string]interface{}{"failure_threshold": 50}},
		{Name: "cors", Enabled: true, Order: 2, Config: map[string]interface{}{"allowed_origins": []interface{}{"*"}}},
	}
	if err := mgr.LoadAvailablePlugins(newConfigs); err != nil {
		t.Fatalf("重新加载插件失败: %v", err)
	}

	// rate_limit 必须已被从 availablePlugins 中摘除
	if _, ok := mgr.GetAvailablePlugin("rate_limit"); ok {
		t.Fatalf("rate_limit 应已从可用插件中被移除")
	}

	// 此时若有路由尝试加载已移除的 rate_limit，必须返回明确错误
	err := mgr.LoadRoutePlugins("route-2", []string{"rate_limit"})
	if err == nil {
		t.Fatalf("路由引用已摘除的插件时必须报错")
	}
	expectedErrMsg := "路由 route-2 使用的插件 rate_limit 不可用"
	if err.Error() != expectedErrMsg {
		t.Fatalf("期望错误提示 '%s'，实际为 '%v'", expectedErrMsg, err)
	}

	_ = mgr.StopAll()
}
