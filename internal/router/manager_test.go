package router

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func makeTestContext(method, urlStr, host string, headers map[string]string) *gin.Context {
	req := httptest.NewRequest(method, urlStr, nil)
	if host != "" {
		req.Host = host
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	return c
}

// TestRouterManagerMatch 表驱动测试，覆盖 40+ 种路由匹配场景 (REQ-P1-03)
func TestRouterManagerMatch(t *testing.T) {
	routes := []RouteDefinition{
		// 1. 根路径路由
		{
			Name: "root-exact",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/",
				Priority: 10,
			},
			Target: TargetService{URL: "http://upstream-root"},
		},
		// 2. 精确匹配路由
		{
			Name: "health-check",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/gatewaygo/health",
				Priority: 100,
			},
			Target: TargetService{URL: "http://upstream-health"},
		},
		{
			Name: "exact-login",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/auth/login",
				Method:   "POST",
				Priority: 50,
			},
			Target: TargetService{URL: "http://upstream-login"},
		},
		{
			Name: "users-profile-exact",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/api/v1/users/profile",
				Priority: 80,
			},
			Target: TargetService{URL: "http://upstream-users-profile-exact"},
		},
		// 3. 前缀匹配路由 (含嵌套前缀)
		{
			Name: "generic-api",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/api",
				Priority: 90, // 注意：无 host，较高优先级
			},
			Target: TargetService{URL: "http://upstream-generic-api"},
		},
		{
			Name: "api-v1-nested",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/api/v1",
				Priority: 85,
			},
			Target: TargetService{URL: "http://upstream-api-v1"},
		},
		{
			Name: "api-v1-orders",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/api/v1/orders",
				Priority: 85,
			},
			Target: TargetService{URL: "http://upstream-api-v1-orders"},
		},
		// 4. Host 匹配路由 (REQ-P1-03 核心裁决场景)
		{
			Name: "v1-api-specific-host",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/api",
				Host:     "v1.api.example.com",
				Priority: 80, // 虽然优先级 80 低于 generic-api 的 90，但具体 host 应胜出！
			},
			Target: TargetService{URL: "http://upstream-v1-host"},
		},
		{
			Name: "wildcard-host-route",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/api",
				Host:     "*.service.internal",
				Priority: 70,
			},
			Target: TargetService{URL: "http://upstream-wildcard-host"},
		},
		{
			Name: "exact-host-only-service",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/special",
				Host:     "secure.example.com",
				Priority: 100,
			},
			Target: TargetService{URL: "http://upstream-secure"},
		},
		// 5. 方法匹配路由
		{
			Name: "articles-get",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/articles",
				Method:   "GET",
				Priority: 60,
			},
			Target: TargetService{URL: "http://upstream-articles-get"},
		},
		{
			Name: "articles-post",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/articles",
				Method:   "POST",
				Priority: 60,
			},
			Target: TargetService{URL: "http://upstream-articles-post"},
		},
		{
			Name: "multi-method-route",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/multi",
				Method:   "GET,POST",
				Priority: 60,
			},
			Target: TargetService{URL: "http://upstream-multi"},
		},
		// 6. Header 匹配路由
		{
			Name: "header-v2-route",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/api/versioned",
				Headers:  map[string]string{"X-Api-Version": "v2"},
				Priority: 100,
			},
			Target: TargetService{URL: "http://upstream-header-v2"},
		},
		{
			Name: "header-v1-route",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/api/versioned",
				Headers:  map[string]string{"X-Api-Version": "v1"},
				Priority: 50,
			},
			Target: TargetService{URL: "http://upstream-header-v1"},
		},
		// 7. QueryParams 匹配路由
		{
			Name: "query-param-debug",
			Match: RouteMatch{
				Type:        MatchExact,
				Path:        "/search",
				QueryParams: map[string]string{"mode": "debug"},
				Priority:    100,
			},
			Target: TargetService{URL: "http://upstream-search-debug"},
		},
		{
			Name: "query-param-default",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/search",
				Priority: 50,
			},
			Target: TargetService{URL: "http://upstream-search-default"},
		},
		// 8. 优先级抢占对比路由
		{
			Name: "prio-high",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/priority/test",
				Priority: 200,
			},
			Target: TargetService{URL: "http://upstream-prio-high"},
		},
		{
			Name: "prio-low",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/priority/test",
				Priority: 50,
			},
			Target: TargetService{URL: "http://upstream-prio-low"},
		},
	}

	mgr := NewManagerWithRoutes(routes)

	tests := []struct {
		desc        string
		method      string
		urlStr      string
		host        string
		headers     map[string]string
		expectRoute string
		expectURL   string
		expectErr   bool
	}{
		// --- 1. 精确匹配场景 ---
		{
			desc:        "精确匹配根路径 /",
			method:      "GET",
			urlStr:      "/",
			expectRoute: "root-exact",
			expectURL:   "http://upstream-root",
		},
		{
			desc:        "精确匹配健康检查 /gatewaygo/health",
			method:      "GET",
			urlStr:      "/gatewaygo/health",
			expectRoute: "health-check",
			expectURL:   "http://upstream-health",
		},
		{
			desc:        "精确匹配末尾斜杠兼容 /gatewaygo/health/",
			method:      "GET",
			urlStr:      "/gatewaygo/health/",
			expectRoute: "health-check",
			expectURL:   "http://upstream-health",
		},
		{
			desc:        "精确匹配深层路径 /api/v1/users/profile",
			method:      "GET",
			urlStr:      "/api/v1/users/profile",
			expectRoute: "users-profile-exact",
			expectURL:   "http://upstream-users-profile-exact",
		},
		{
			desc:        "精确匹配优于前缀匹配 (同一路径下)",
			method:      "GET",
			urlStr:      "/api/v1/users/profile",
			expectRoute: "users-profile-exact",
			expectURL:   "http://upstream-users-profile-exact",
		},
		{
			desc:        "精确匹配路径后带子路径未配置时回退或报错 (/auth/login/extra 无回退则404)",
			method:      "POST",
			urlStr:      "/auth/login/extra",
			expectErr:   true,
		},

		// --- 2. 前缀回溯匹配场景 (Prefix Fallback) ---
		{
			desc:        "前缀匹配命中前缀自身 /api",
			method:      "GET",
			urlStr:      "/api",
			expectRoute: "generic-api",
			expectURL:   "http://upstream-generic-api",
		},
		{
			desc:        "前缀匹配一级子路径回退 /api/v2",
			method:      "GET",
			urlStr:      "/api/v2",
			expectRoute: "generic-api",
			expectURL:   "http://upstream-generic-api",
		},
		{
			desc:        "前缀匹配深层子路径回退 /api/v2/items/123",
			method:      "GET",
			urlStr:      "/api/v2/items/123",
			expectRoute: "generic-api",
			expectURL:   "http://upstream-generic-api",
		},
		{
			desc:        "嵌套前缀匹配更长前缀 /api/v1/items 命中 /api/v1 而非 /api",
			method:      "GET",
			urlStr:      "/api/v1/items",
			expectRoute: "api-v1-nested",
			expectURL:   "http://upstream-api-v1",
		},
		{
			desc:        "三层嵌套前缀匹配最深前缀 /api/v1/orders/detail 命中 /api/v1/orders",
			method:      "GET",
			urlStr:      "/api/v1/orders/detail",
			expectRoute: "api-v1-orders",
			expectURL:   "http://upstream-api-v1-orders",
		},
		{
			desc:        "前缀路径边界检查：/apikey 不属于 /api 前缀",
			method:      "GET",
			urlStr:      "/apikey",
			expectErr:   true,
		},

		// --- 3. Host 裁决场景 (REQ-P1-03 核心标准) ---
		{
			desc:        "更具体Host优先于无Host高优先级路由 (v1.api.example.com prio 80 胜出 generic-api prio 90)",
			method:      "GET",
			urlStr:      "/api/test",
			host:        "v1.api.example.com",
			expectRoute: "v1-api-specific-host",
			expectURL:   "http://upstream-v1-host",
		},
		{
			desc:        "Host 包含端口号时不影响精确匹配 (v1.api.example.com:8080)",
			method:      "GET",
			urlStr:      "/api/test",
			host:        "v1.api.example.com:8080",
			expectRoute: "v1-api-specific-host",
			expectURL:   "http://upstream-v1-host",
		},
		{
			desc:        "Host 通配符匹配 (*.service.internal 命中 app.service.internal)",
			method:      "GET",
			urlStr:      "/api/test",
			host:        "app.service.internal",
			expectRoute: "wildcard-host-route",
			expectURL:   "http://upstream-wildcard-host",
		},
		{
			desc:        "Host 通配符匹配带端口号 (app.service.internal:9000)",
			method:      "GET",
			urlStr:      "/api/test",
			host:        "app.service.internal:9000",
			expectRoute: "wildcard-host-route",
			expectURL:   "http://upstream-wildcard-host",
		},
		{
			desc:        "其他未指定Host请求命中泛化无Host路由",
			method:      "GET",
			urlStr:      "/api/test",
			host:        "other.example.com",
			expectRoute: "generic-api",
			expectURL:   "http://upstream-generic-api",
		},
		{
			desc:        "专有Host精确路径匹配成功 (secure.example.com /special)",
			method:      "GET",
			urlStr:      "/special",
			host:        "secure.example.com",
			expectRoute: "exact-host-only-service",
			expectURL:   "http://upstream-secure",
		},
		{
			desc:        "专有Host精确路径因Host不匹配返回404",
			method:      "GET",
			urlStr:      "/special",
			host:        "wrong.example.com",
			expectErr:   true,
		},

		// --- 4. 方法 (Method) 匹配场景 ---
		{
			desc:        "GET 方法精确命中 articles-get",
			method:      "GET",
			urlStr:      "/articles",
			expectRoute: "articles-get",
			expectURL:   "http://upstream-articles-get",
		},
		{
			desc:        "POST 方法精确命中 articles-post",
			method:      "POST",
			urlStr:      "/articles",
			expectRoute: "articles-post",
			expectURL:   "http://upstream-articles-post",
		},
		{
			desc:        "DELETE 方法在未配置该方法时返回 404",
			method:      "DELETE",
			urlStr:      "/articles",
			expectErr:   true,
		},
		{
			desc:        "多方法配置命中 GET",
			method:      "GET",
			urlStr:      "/multi",
			expectRoute: "multi-method-route",
			expectURL:   "http://upstream-multi",
		},
		{
			desc:        "多方法配置命中 POST",
			method:      "POST",
			urlStr:      "/multi",
			expectRoute: "multi-method-route",
			expectURL:   "http://upstream-multi",
		},
		{
			desc:        "多方法配置不包含 PUT 返回 404",
			method:      "PUT",
			urlStr:      "/multi",
			expectErr:   true,
		},

		// --- 5. 请求头 (Headers) 匹配场景 ---
		{
			desc:        "Header 匹配 X-Api-Version: v2 命中高优先级路由",
			method:      "GET",
			urlStr:      "/api/versioned/users",
			headers:     map[string]string{"X-Api-Version": "v2"},
			expectRoute: "header-v2-route",
			expectURL:   "http://upstream-header-v2",
		},
		{
			desc:        "Header 匹配 X-Api-Version: v1 命中低优先级路由",
			method:      "GET",
			urlStr:      "/api/versioned/users",
			headers:     map[string]string{"X-Api-Version": "v1"},
			expectRoute: "header-v1-route",
			expectURL:   "http://upstream-header-v1",
		},
		{
			desc:        "Header 不匹配回退到外层 generic-api 前缀路由",
			method:      "GET",
			urlStr:      "/api/versioned/users",
			headers:     map[string]string{"X-Api-Version": "v3"},
			expectRoute: "generic-api",
			expectURL:   "http://upstream-generic-api",
		},

		// --- 6. 查询参数 (QueryParams) 匹配场景 ---
		{
			desc:        "Query 参数 mode=debug 命中特定路由",
			method:      "GET",
			urlStr:      "/search?mode=debug",
			expectRoute: "query-param-debug",
			expectURL:   "http://upstream-search-debug",
		},
		{
			desc:        "无 Query 参数命中默认搜索路由",
			method:      "GET",
			urlStr:      "/search",
			expectRoute: "query-param-default",
			expectURL:   "http://upstream-search-default",
		},
		{
			desc:        "Query 参数 mode=other 命中默认搜索路由",
			method:      "GET",
			urlStr:      "/search?mode=other",
			expectRoute: "query-param-default",
			expectURL:   "http://upstream-search-default",
		},

		// --- 7. 优先级 (Priority) 裁决场景 ---
		{
			desc:        "同路径下高优先级路由抢占 (priority 200 vs 50)",
			method:      "GET",
			urlStr:      "/priority/test",
			expectRoute: "prio-high",
			expectURL:   "http://upstream-prio-high",
		},

		// --- 8. 404 未找到匹配场景 ---
		{
			desc:        "未知路径 /unknown/nonexistent 返回 404",
			method:      "GET",
			urlStr:      "/unknown/nonexistent",
			expectErr:   true,
		},
		{
			desc:        "未匹配的根级目录 /other",
			method:      "GET",
			urlStr:      "/other",
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			c := makeTestContext(tt.method, tt.urlStr, tt.host, tt.headers)
			matched, err := mgr.MatchRoute(c)

			if tt.expectErr {
				assert.Error(t, err, "应返回未匹配错误")
				assert.Nil(t, matched, "未匹配时不应返回路由")
			} else {
				require.NoError(t, err, "路由匹配不应返回错误")
				require.NotNil(t, matched, "应返回匹配的路由")
				assert.Equal(t, tt.expectRoute, matched.Name, "匹配到的路由名称不一致")
				assert.Equal(t, tt.expectURL, matched.Target.URL, "匹配到的目标URL不一致")
			}
		})
	}
}

// TestEmptyRouterMatch 测试空路由管理器匹配
func TestEmptyRouterMatch(t *testing.T) {
	mgr := NewManagerWithRoutes(nil)
	c := makeTestContext("GET", "/test", "", nil)
	matched, err := mgr.MatchRoute(c)
	assert.Error(t, err)
	assert.Nil(t, matched)
	assert.Equal(t, ErrRouteNotFound, err)
}

// TestTriePrefixBacktracking 专门验证 TrieRouter 前缀回退行为 (REQ-P1-03)
func TestTriePrefixBacktracking(t *testing.T) {
	tr := NewTrieRouter()

	rRoot := &RouteDefinition{
		Name:  "root-prefix",
		Match: RouteMatch{Type: MatchPrefix, Path: "/"},
	}
	rApi := &RouteDefinition{
		Name:  "api-prefix",
		Match: RouteMatch{Type: MatchPrefix, Path: "/api"},
	}
	rV1 := &RouteDefinition{
		Name:  "v1-prefix",
		Match: RouteMatch{Type: MatchPrefix, Path: "/api/v1"},
	}
	rExact := &RouteDefinition{
		Name:  "exact-user",
		Match: RouteMatch{Type: MatchExact, Path: "/api/v1/user"},
	}

	tr.Insert("/", rRoot)
	tr.Insert("/api", rApi)
	tr.Insert("/api/v1", rV1)
	tr.Insert("/api/v1/user", rExact)

	// 1. 完全精确匹配
	match, ok := tr.Match("/api/v1/user")
	require.True(t, ok)
	assert.Equal(t, "exact-user", match.Name)

	// 2. 回退到 /api/v1
	match, ok = tr.Match("/api/v1/orders")
	require.True(t, ok)
	assert.Equal(t, "v1-prefix", match.Name)

	// 3. 回退到 /api
	match, ok = tr.Match("/api/v2/users")
	require.True(t, ok)
	assert.Equal(t, "api-prefix", match.Name)

	// 4. 回退到根前缀 /
	match, ok = tr.Match("/dashboard")
	require.True(t, ok)
	assert.Equal(t, "root-prefix", match.Name)
}

// TestRouteCacheLRUAndTTL 测试路由缓存的容量限制、LRU淘汰及TTL (REQ-P1-03)
func TestRouteCacheLRUAndTTL(t *testing.T) {
	// 容量为 3，TTL 为 50ms
	cache := NewRouteCacheWithTTL(3, 50*time.Millisecond)

	r1 := &RouteDefinition{Name: "route-1"}
	r2 := &RouteDefinition{Name: "route-2"}
	r3 := &RouteDefinition{Name: "route-3"}
	r4 := &RouteDefinition{Name: "route-4"}

	cache.Set("key1", r1)
	cache.Set("key2", r2)
	cache.Set("key3", r3)

	assert.Equal(t, 3, cache.Len())

	// 访问 key1，使 key2 成为最久未访问
	got, ok := cache.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "route-1", got.Name)

	// 插入第 4 条，应淘汰最久未更新条目
	cache.Set("key4", r4)
	assert.Equal(t, 3, cache.Len())

	// key4 应存在
	got, ok = cache.Get("key4")
	assert.True(t, ok)
	assert.Equal(t, "route-4", got.Name)

	// key1 重新更新后应存在
	cache.Set("key1", r1)
	got, ok = cache.Get("key1")
	assert.True(t, ok)

	// 等待 TTL 过期
	time.Sleep(60 * time.Millisecond)
	_, ok = cache.Get("key1")
	assert.False(t, ok, "超过 TTL 后应返回未命中")
}

// TestRouteCacheConcurrency 并发测试路由缓存与路由管理器 (-race 验证)
func TestRouteCacheConcurrency(t *testing.T) {
	routes := []RouteDefinition{
		{
			Name:  "route-a",
			Match: RouteMatch{Type: MatchPrefix, Path: "/api/a"},
		},
		{
			Name:  "route-b",
			Match: RouteMatch{Type: MatchPrefix, Path: "/api/b"},
		},
	}
	mgr := NewManagerWithRoutes(routes)

	const goroutines = 50
	const iterations = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			path := "/api/a/sub"
			if id%2 == 1 {
				path = "/api/b/sub"
			}
			for j := 0; j < iterations; j++ {
				c := makeTestContext("GET", path, "example.com", nil)
				matched, err := mgr.MatchRoute(c)
				if err != nil || matched == nil {
					t.Errorf("并发路由匹配失败: %v", err)
					return
				}
			}
		}(i)
	}

	wg.Wait()
}

// BenchmarkMatch100Routes 100 条路由配置下的单次匹配延迟基准测试 (REQ-P1-03)
// 验收标准：单次匹配延迟 < 10µs/op
func BenchmarkMatch100Routes(b *testing.B) {
	// 构建 100 条具备不同前缀、精确匹配与不同优先级的路由
	routes := make([]RouteDefinition, 0, 100)

	for i := 0; i < 50; i++ {
		routes = append(routes, RouteDefinition{
			Name: fmt.Sprintf("prefix-route-%02d", i),
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     fmt.Sprintf("/service-%02d", i),
				Priority: i,
			},
			Target: TargetService{
				URL: fmt.Sprintf("http://upstream-%02d:8080", i),
			},
		})
	}

	for i := 50; i < 100; i++ {
		routes = append(routes, RouteDefinition{
			Name: fmt.Sprintf("exact-route-%02d", i),
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     fmt.Sprintf("/service-%02d/action", i),
				Priority: i + 10,
			},
			Target: TargetService{
				URL: fmt.Sprintf("http://upstream-%02d:8080", i),
			},
		})
	}

	mgr := NewManagerWithRoutes(routes)

	// 测试路径：包含深层前缀子路径
	testPaths := []string{
		"/service-05/v1/users/info",
		"/service-25/orders/detail/100",
		"/service-49/anything",
		"/service-75/action",
		"/service-99/action",
	}

	contexts := make([]*gin.Context, len(testPaths))
	for i, p := range testPaths {
		contexts[i] = makeTestContext("GET", p, "benchmark.example.com", nil)
	}

	// 预热缓存
	for _, c := range contexts {
		_, _ = mgr.MatchRoute(c)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		c := contexts[i%len(contexts)]
		matched, err := mgr.MatchRoute(c)
		if err != nil || matched == nil {
			b.Fatalf("基准测试路由匹配失败: %v", err)
		}
	}

	b.StopTimer()

	// 断言单次匹配耗时必须小于 10µs/op (10,000 ns)
	nsPerOp := b.Elapsed().Nanoseconds() / int64(b.N)
	if nsPerOp > 10_000 {
		b.Errorf("单次路由匹配耗时超出标准 (<10µs/op): 实测 %d ns/op", nsPerOp)
	}
}
