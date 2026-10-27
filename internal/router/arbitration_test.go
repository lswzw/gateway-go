package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"gateway-go/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestArbitrationAndRoutingSemantics 测试路由匹配语义与标准裁决顺序 (REQ-P1-03)
// 标准裁决顺序:
// 1) Host exact match > Host wildcard match > No Host (wildcard/generic)
// 2) Route priority descending
// 3) Path length / specificity descending
func TestArbitrationAndRoutingSemantics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 构建覆盖全场景的路由定义列表
	routes := []RouteDefinition{
		// ---------------------------------------------------------------------
		// 1. Host 作用域对比路由
		// ---------------------------------------------------------------------
		{
			Name: "exact-host-v1",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/api",
				Host:     "v1.api.example.com",
				Priority: 80, // 虽然优先级 80 低于 generic-api 的 90，但精确 Host 必须胜出
			},
			Target: TargetService{URL: "http://upstream-v1-exact-host"},
		},
		{
			Name: "wildcard-host-service",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/api",
				Host:     "*.service.internal",
				Priority: 85, // 通配符 Host 胜过无 Host 路由
			},
			Target: TargetService{URL: "http://upstream-wildcard-host"},
		},
		{
			Name: "generic-api",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/api",
				Priority: 90, // 无 Host 泛化路由
			},
			Target: TargetService{URL: "http://upstream-generic-api"},
		},
		{
			Name: "special-secure-host",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/secure/token",
				Host:     "auth.example.com",
				Priority: 100,
			},
			Target: TargetService{URL: "http://upstream-secure-auth"},
		},

		// ---------------------------------------------------------------------
		// 2. 优先级排序路由 (同 Host / 无 Host 场景)
		// ---------------------------------------------------------------------
		{
			Name: "prio-high-route",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/test/priority",
				Priority: 200,
			},
			Target: TargetService{URL: "http://upstream-prio-high"},
		},
		{
			Name: "prio-low-route",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/test/priority",
				Priority: 50,
			},
			Target: TargetService{URL: "http://upstream-prio-low"},
		},
		{
			Name: "prio-override-specific",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/catalog/items",
				Priority: 150, // 高优先级子路径
			},
			Target: TargetService{URL: "http://upstream-catalog-specific"},
		},
		{
			Name: "prio-override-broad",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/catalog",
				Priority: 100, // 较低优先级父路径
			},
			Target: TargetService{URL: "http://upstream-catalog-broad"},
		},

		// ---------------------------------------------------------------------
		// 3. 同优先级下路径特异性与长度裁决路由
		// ---------------------------------------------------------------------
		{
			Name: "path-exact-order",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/orders/checkout",
				Priority: 90,
			},
			Target: TargetService{URL: "http://upstream-orders-exact"},
		},
		{
			Name: "path-prefix-orders",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/orders",
				Priority: 90,
			},
			Target: TargetService{URL: "http://upstream-orders-prefix"},
		},
		{
			Name: "deep-nested-lvl1",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/services/finance",
				Priority: 90,
			},
			Target: TargetService{URL: "http://upstream-finance-lvl1"},
		},
		{
			Name: "deep-nested-lvl2",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/services/finance/transfers",
				Priority: 90,
			},
			Target: TargetService{URL: "http://upstream-finance-lvl2"},
		},
		{
			Name: "deep-nested-lvl3",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/services/finance/transfers/crypto",
				Priority: 90,
			},
			Target: TargetService{URL: "http://upstream-finance-lvl3"},
		},

		// ---------------------------------------------------------------------
		// 4. 正则表达式路由 (Regex)
		// ---------------------------------------------------------------------
		{
			Name: "regex-user-id",
			Match: RouteMatch{
				Type:     MatchRegex,
				Path:     `^/users/(\d+)$`,
				Priority: 110,
			},
			Target: TargetService{URL: "http://upstream-user-regex"},
		},
		{
			Name: "regex-article-date",
			Match: RouteMatch{
				Type:     MatchRegex,
				Path:     `^/articles/(?P<year>\d{4})/(?P<slug>[a-z0-9-]+)$`,
				Priority: 110,
			},
			Target: TargetService{URL: "http://upstream-article-regex"},
		},
		{
			Name: "regex-fallback-prefix",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/users",
				Priority: 50,
			},
			Target: TargetService{URL: "http://upstream-users-fallback"},
		},

		// ---------------------------------------------------------------------
		// 5. 通配符路由 (Wildcard Glob)
		// ---------------------------------------------------------------------
		{
			Name: "wildcard-mid-single",
			Match: RouteMatch{
				Type:     MatchWildcard,
				Path:     "/products/*/reviews",
				Priority: 120,
			},
			Target: TargetService{URL: "http://upstream-wildcard-mid"},
		},
		{
			Name: "wildcard-end-files",
			Match: RouteMatch{
				Type:     MatchWildcard,
				Path:     "/documents/*",
				Priority: 120,
			},
			Target: TargetService{URL: "http://upstream-wildcard-end"},
		},
		{
			Name: "wildcard-multi-static",
			Match: RouteMatch{
				Type:     MatchWildcard,
				Path:     "/assets/**",
				Priority: 120,
			},
			Target: TargetService{URL: "http://upstream-wildcard-multi"},
		},
		{
			Name: "wildcard-png-ext",
			Match: RouteMatch{
				Type:     MatchWildcard,
				Path:     "/images/*.png",
				Priority: 120,
			},
			Target: TargetService{URL: "http://upstream-wildcard-png"},
		},
		{
			Name: "wildcard-dot-prefix",
			Match: RouteMatch{
				Type:     MatchWildcard,
				Path:     "/v1.0/*",
				Priority: 120,
			},
			Target: TargetService{URL: "http://upstream-wildcard-dot"},
		},

		// ---------------------------------------------------------------------
		// 6. A/B 测试路由
		// ---------------------------------------------------------------------
		{
			Name: "ab-test-weight",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/experiments/weight",
				Priority: 130,
				ABTest: &ABTestConfig{
					Enabled:      true,
					WeightA:      100,
					WeightB:      0,
					GroupATarget: "http://upstream-ab-weight-a",
					GroupBTarget: "http://upstream-ab-weight-b",
				},
			},
			Target: TargetService{URL: "http://upstream-ab-weight-default"},
		},
		{
			Name: "ab-test-percent",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/experiments/percent",
				Priority: 130,
				ABTest: &ABTestConfig{
					Enabled:      true,
					GroupA:       1.0, // 100% Group A
					GroupB:       0.0,
					GroupATarget: "http://upstream-ab-percent-a",
					GroupBTarget: "http://upstream-ab-percent-b",
				},
			},
			Target: TargetService{URL: "http://upstream-ab-percent-default"},
		},
		{
			Name: "ab-test-bucket-header",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/experiments/bucket",
				Priority: 130,
				ABTest: &ABTestConfig{
					Enabled:      true,
					BucketBy:     "header:X-User-Tier",
					WeightA:      0,
					WeightB:      100, // 100% Group B
					GroupATarget: "http://upstream-ab-header-a",
					GroupBTarget: "http://upstream-ab-header-b",
				},
			},
			Target: TargetService{URL: "http://upstream-ab-header-default"},
		},
		{
			Name: "ab-test-disabled",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/experiments/disabled",
				Priority: 130,
				ABTest: &ABTestConfig{
					Enabled:      false,
					GroupATarget: "http://upstream-ab-should-not-hit",
				},
			},
			Target: TargetService{URL: "http://upstream-ab-disabled-default"},
		},

		// ---------------------------------------------------------------------
		// 7. Method 与 Header 条件过滤路由
		// ---------------------------------------------------------------------
		{
			Name: "method-get-items",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/resource/items",
				Method:   "GET",
				Priority: 90,
			},
			Target: TargetService{URL: "http://upstream-get-items"},
		},
		{
			Name: "method-post-items",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/resource/items",
				Method:   "POST",
				Priority: 90,
			},
			Target: TargetService{URL: "http://upstream-post-items"},
		},
		{
			Name: "header-canary-active",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/canary/feature",
				Headers:  map[string]string{"X-Canary": "always"},
				Priority: 90,
			},
			Target: TargetService{URL: "http://upstream-canary-active"},
		},
		{
			Name: "header-canary-default",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/canary/feature",
				Priority: 90,
			},
			Target: TargetService{URL: "http://upstream-canary-default"},
		},
	}

	mgr := NewManagerWithRoutes(routes)
	require.NotNil(t, mgr)

	// 表驱动测试用例：覆盖 >= 35 个关键场景
	testCases := []struct {
		desc        string
		method      string
		urlPath     string
		host        string
		headers     map[string]string
		expectRoute string
		expectURL   string
		expectErr   bool
	}{
		// ---------------------------------------------------------------------
		// Category 1: Host 裁决规则 (Host Exact > Host Wildcard > No Host)
		// ---------------------------------------------------------------------
		{
			desc:        "Case 1: 精确 Host 匹配胜过无 Host 高优先级路由 (v1.api.example.com prio 80 > generic-api prio 90)",
			method:      "GET",
			urlPath:     "/api/users",
			host:        "v1.api.example.com",
			expectRoute: "exact-host-v1",
			expectURL:   "http://upstream-v1-exact-host",
		},
		{
			desc:        "Case 2: 精确 Host 匹配忽略端口号 (v1.api.example.com:8443)",
			method:      "GET",
			urlPath:     "/api/orders",
			host:        "v1.api.example.com:8443",
			expectRoute: "exact-host-v1",
			expectURL:   "http://upstream-v1-exact-host",
		},
		{
			desc:        "Case 3: 通配符 Host 匹配 (*.service.internal 命中 auth.service.internal)",
			method:      "GET",
			urlPath:     "/api/token",
			host:        "auth.service.internal",
			expectRoute: "wildcard-host-service",
			expectURL:   "http://upstream-wildcard-host",
		},
		{
			desc:        "Case 4: 通配符 Host 匹配带端口号 (billing.service.internal:9000)",
			method:      "GET",
			urlPath:     "/api/pay",
			host:        "billing.service.internal:9000",
			expectRoute: "wildcard-host-service",
			expectURL:   "http://upstream-wildcard-host",
		},
		{
			desc:        "Case 5: 无 Host 限定的请求命中通用兜底 API 路由",
			method:      "GET",
			urlPath:     "/api/health",
			host:        "other.example.com",
			expectRoute: "generic-api",
			expectURL:   "http://upstream-generic-api",
		},
		{
			desc:        "Case 6: 专有 Host 精确路径命中 (auth.example.com /secure/token)",
			method:      "POST",
			urlPath:     "/secure/token",
			host:        "auth.example.com",
			expectRoute: "special-secure-host",
			expectURL:   "http://upstream-secure-auth",
		},
		{
			desc:        "Case 7: 专有 Host 请求未匹配对应路径返回 404",
			method:      "GET",
			urlPath:     "/secure/unknown",
			host:        "auth.example.com",
			expectErr:   true,
		},

		// ---------------------------------------------------------------------
		// Category 2: 优先级排序 (Priority Descending)
		// ---------------------------------------------------------------------
		{
			desc:        "Case 8: 相同路径下高优先级抢占 (prio 200 胜出 prio 50)",
			method:      "GET",
			urlPath:     "/test/priority",
			expectRoute: "prio-high-route",
			expectURL:   "http://upstream-prio-high",
		},
		{
			desc:        "Case 9: 较长前缀因高优先级胜出 (/catalog/items prio 150 > /catalog prio 100)",
			method:      "GET",
			urlPath:     "/catalog/items/456",
			expectRoute: "prio-override-specific",
			expectURL:   "http://upstream-catalog-specific",
		},
		{
			desc:        "Case 10: 访问未覆盖子路径命中较短前缀 (/catalog/other)",
			method:      "GET",
			urlPath:     "/catalog/other",
			expectRoute: "prio-override-broad",
			expectURL:   "http://upstream-catalog-broad",
		},

		// ---------------------------------------------------------------------
		// Category 3: 同优先级下路径特异性与长度裁决
		// ---------------------------------------------------------------------
		{
			desc:        "Case 11: 同优先级下精确匹配胜过前缀匹配 (/orders/checkout exact > prefix)",
			method:      "POST",
			urlPath:     "/orders/checkout",
			expectRoute: "path-exact-order",
			expectURL:   "http://upstream-orders-exact",
		},
		{
			desc:        "Case 12: 精确匹配末尾斜杠兼容匹配 (/orders/checkout/)",
			method:      "POST",
			urlPath:     "/orders/checkout/",
			expectRoute: "path-exact-order",
			expectURL:   "http://upstream-orders-exact",
		},
		{
			desc:        "Case 13: 访问普通前缀路径命中 (/orders/list)",
			method:      "GET",
			urlPath:     "/orders/list",
			expectRoute: "path-prefix-orders",
			expectURL:   "http://upstream-orders-prefix",
		},
		{
			desc:        "Case 14: 三层嵌套前缀命中三层最深路径 (/services/finance/transfers/crypto/btc)",
			method:      "GET",
			urlPath:     "/services/finance/transfers/crypto/btc",
			expectRoute: "deep-nested-lvl3",
			expectURL:   "http://upstream-finance-lvl3",
		},
		{
			desc:        "Case 15: 二层嵌套前缀命中二层路径 (/services/finance/transfers/fiat)",
			method:      "GET",
			urlPath:     "/services/finance/transfers/fiat",
			expectRoute: "deep-nested-lvl2",
			expectURL:   "http://upstream-finance-lvl2",
		},
		{
			desc:        "Case 16: 一层嵌套前缀命中一层路径 (/services/finance/reports)",
			method:      "GET",
			urlPath:     "/services/finance/reports",
			expectRoute: "deep-nested-lvl1",
			expectURL:   "http://upstream-finance-lvl1",
		},

		// ---------------------------------------------------------------------
		// Category 4: 正则表达式匹配 (Regex)
		// ---------------------------------------------------------------------
		{
			desc:        "Case 17: 正则数字ID路径匹配成功 (/users/123456)",
			method:      "GET",
			urlPath:     "/users/123456",
			expectRoute: "regex-user-id",
			expectURL:   "http://upstream-user-regex",
		},
		{
			desc:        "Case 18: 非纯数字路径不匹配数字正则，回退至前缀路由 (/users/profile)",
			method:      "GET",
			urlPath:     "/users/profile",
			expectRoute: "regex-fallback-prefix",
			expectURL:   "http://upstream-users-fallback",
		},
		{
			desc:        "Case 19: 多捕获组复杂正则匹配成功 (/articles/2026/go-release)",
			method:      "GET",
			urlPath:     "/articles/2026/go-release",
			expectRoute: "regex-article-date",
			expectURL:   "http://upstream-article-regex",
		},
		{
			desc:        "Case 20: 复杂正则年份不符合规则返回 404 (/articles/26/go-release)",
			method:      "GET",
			urlPath:     "/articles/26/go-release",
			expectErr:   true,
		},
		{
			desc:        "Case 21: 复杂正则含有非法特殊字符返回 404 (/articles/2026/bad_slug!)",
			method:      "GET",
			urlPath:     "/articles/2026/bad_slug!",
			expectErr:   true,
		},

		// ---------------------------------------------------------------------
		// Category 5: 通配符匹配 (Wildcard Glob 语义)
		// ---------------------------------------------------------------------
		{
			desc:        "Case 22: 中间单段通配符匹配成功 (/products/keyboard-pro/reviews)",
			method:      "GET",
			urlPath:     "/products/keyboard-pro/reviews",
			expectRoute: "wildcard-mid-single",
			expectURL:   "http://upstream-wildcard-mid",
		},
		{
			desc:        "Case 23: 中间单段通配符拒绝跨段多斜杠路径 (/products/hardware/mouse/reviews)",
			method:      "GET",
			urlPath:     "/products/hardware/mouse/reviews",
			expectErr:   true,
		},
		{
			desc:        "Case 24: 末尾单段通配符匹配单级文件 (/documents/readme.pdf)",
			method:      "GET",
			urlPath:     "/documents/readme.pdf",
			expectRoute: "wildcard-end-files",
			expectURL:   "http://upstream-wildcard-end",
		},
		{
			desc:        "Case 25: 末尾单段通配符匹配单级目录 (/documents/report)",
			method:      "GET",
			urlPath:     "/documents/report",
			expectRoute: "wildcard-end-files",
			expectURL:   "http://upstream-wildcard-end",
		},
		{
			desc:        "Case 26: 末尾单段通配符拒绝多级深层子目录 (/documents/2026/annual/report.pdf)",
			method:      "GET",
			urlPath:     "/documents/2026/annual/report.pdf",
			expectErr:   true,
		},
		{
			desc:        "Case 27: 多级通配符 ** 匹配深层静态资源 (/assets/css/theme/dark.css)",
			method:      "GET",
			urlPath:     "/assets/css/theme/dark.css",
			expectRoute: "wildcard-multi-static",
			expectURL:   "http://upstream-wildcard-multi",
		},
		{
			desc:        "Case 28: 多级通配符 ** 匹配单级资源 (/assets/favicon.ico)",
			method:      "GET",
			urlPath:     "/assets/favicon.ico",
			expectRoute: "wildcard-multi-static",
			expectURL:   "http://upstream-wildcard-multi",
		},
		{
			desc:        "Case 29: 后缀通配符匹配对应扩展名文件 (/images/avatar.png)",
			method:      "GET",
			urlPath:     "/images/avatar.png",
			expectRoute: "wildcard-png-ext",
			expectURL:   "http://upstream-wildcard-png",
		},
		{
			desc:        "Case 30: 后缀通配符拒绝非指定扩展名 (/images/avatar.jpg)",
			method:      "GET",
			urlPath:     "/images/avatar.jpg",
			expectErr:   true,
		},
		{
			desc:        "Case 31: 通配符中带点字面量正确转义 (/v1.0/info 匹配 /v1.0/*)",
			method:      "GET",
			urlPath:     "/v1.0/info",
			expectRoute: "wildcard-dot-prefix",
			expectURL:   "http://upstream-wildcard-dot",
		},
		{
			desc:        "Case 32: 通配符中带点字面量不匹配其它字符 (/v1X0/info 不匹配 /v1.0/*)",
			method:      "GET",
			urlPath:     "/v1X0/info",
			expectErr:   true,
		},

		// ---------------------------------------------------------------------
		// Category 6: A/B 测试路由
		// ---------------------------------------------------------------------
		{
			desc:        "Case 33: A/B 测试权重配置 WeightA: 100 命中 Group A",
			method:      "GET",
			urlPath:     "/experiments/weight",
			expectRoute: "ab-test-weight",
			expectURL:   "http://upstream-ab-weight-a",
		},
		{
			desc:        "Case 34: A/B 测试百分比 GroupA: 1.0 (100%) 命中 Group A",
			method:      "GET",
			urlPath:     "/experiments/percent",
			expectRoute: "ab-test-percent",
			expectURL:   "http://upstream-ab-percent-a",
		},
		{
			desc:        "Case 35: A/B 测试指定 BucketBy header 且 WeightB: 100 命中 Group B",
			method:      "GET",
			urlPath:     "/experiments/bucket",
			headers:     map[string]string{"X-User-Tier": "gold-user-123"},
			expectRoute: "ab-test-bucket-header",
			expectURL:   "http://upstream-ab-header-b",
		},
		{
			desc:        "Case 36: A/B 测试未启用时走默认 Target",
			method:      "GET",
			urlPath:     "/experiments/disabled",
			expectRoute: "ab-test-disabled",
			expectURL:   "http://upstream-ab-disabled-default",
		},

		// ---------------------------------------------------------------------
		// Category 7: Method 与 Header 条件过滤
		// ---------------------------------------------------------------------
		{
			desc:        "Case 37: HTTP GET 方法精确匹配 resource-items-get",
			method:      "GET",
			urlPath:     "/resource/items",
			expectRoute: "method-get-items",
			expectURL:   "http://upstream-get-items",
		},
		{
			desc:        "Case 38: HTTP POST 方法精确匹配 resource-items-post",
			method:      "POST",
			urlPath:     "/resource/items",
			expectRoute: "method-post-items",
			expectURL:   "http://upstream-post-items",
		},
		{
			desc:        "Case 39: HTTP DELETE 方法在该路径未配置时返回 404",
			method:      "DELETE",
			urlPath:     "/resource/items",
			expectErr:   true,
		},
		{
			desc:        "Case 40: Header 条件 X-Canary: always 匹配金丝雀路由",
			method:      "GET",
			urlPath:     "/canary/feature/data",
			headers:     map[string]string{"X-Canary": "always"},
			expectRoute: "header-canary-active",
			expectURL:   "http://upstream-canary-active",
		},
		{
			desc:        "Case 41: Header 条件未携带时回退到默认前缀路由",
			method:      "GET",
			urlPath:     "/canary/feature/data",
			expectRoute: "header-canary-default",
			expectURL:   "http://upstream-canary-default",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			req, err := http.NewRequest(tc.method, tc.urlPath, nil)
			require.NoError(t, err)

			if tc.host != "" {
				req.Host = tc.host
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			c.Request = req

			matched, err := mgr.MatchRoute(c)
			if tc.expectErr {
				assert.Error(t, err, "预期匹配失败但未返回错误")
			} else {
				require.NoError(t, err, "匹配失败: %v", err)
				require.NotNil(t, matched, "匹配到的路由为空")
				assert.Equal(t, tc.expectRoute, matched.Name, "匹配到的路由名称不一致")
				assert.Equal(t, tc.expectURL, matched.Target.URL, "匹配到的目标URL不一致")
			}
		})
	}
}

// TestValidateConfig_RegexAndWildcard 测试非法正则表达式在配置加载期校验失败 (REQ-P1-03)
func TestValidateConfig_RegexAndWildcard(t *testing.T) {
	// 1. 测试非法正则表达式 (括号不闭合) 必须返回错误
	invalidRegexCfg := &config.Config{
		Server: config.ServerConfig{
			Port:                    8080,
			ReadTimeout:             10,
			WriteTimeout:            10,
			MaxHeaderBytes:          1024,
			GracefulShutdownTimeout: 5,
		},
		Log: config.LogConfig{
			Level:      "info",
			Format:     "json",
			Output:     "stdout",
			MaxSize:    10,
			MaxAge:     1,
			MaxBackups: 1,
		},
		Routes: []config.RouteConfig{
			{
				Name: "invalid-regex-route",
				Match: config.RouteMatch{
					Type:     "regex",
					Path:     "/api/users/[0-9+", // 非法正则: 缺少闭合括号
					Priority: 100,
				},
				Target: config.TargetConfig{URL: "http://127.0.0.1:8080"},
			},
		},
	}

	err := config.ValidateConfig(invalidRegexCfg)
	assert.Error(t, err, "非法正则表达式必须导致配置验证失败")
	assert.Contains(t, err.Error(), "无效的正则表达式")

	// 2. 测试缺少正则内容
	emptyRegexCfg := &config.Config{
		Server: invalidRegexCfg.Server,
		Log:    invalidRegexCfg.Log,
		Routes: []config.RouteConfig{
			{
				Name: "empty-regex-route",
				Match: config.RouteMatch{
					Type:     "regex",
					Path:     "",
					Priority: 100,
				},
				Target: config.TargetConfig{URL: "http://127.0.0.1:8080"},
			},
		},
	}
	err = config.ValidateConfig(emptyRegexCfg)
	assert.Error(t, err)

	// 3. 测试合法正则与通配符通过校验
	validCfg := &config.Config{
		Server: invalidRegexCfg.Server,
		Log:    invalidRegexCfg.Log,
		Routes: []config.RouteConfig{
			{
				Name: "valid-regex-route",
				Match: config.RouteMatch{
					Type:     "regex",
					Path:     `^/api/v[0-9]+/users/\d+$`,
					Priority: 100,
				},
				Target: config.TargetConfig{URL: "http://127.0.0.1:8080"},
			},
			{
				Name: "valid-wildcard-route",
				Match: config.RouteMatch{
					Type:     "wildcard",
					Path:     "/api/*/detail",
					Priority: 90,
				},
				Target: config.TargetConfig{URL: "http://127.0.0.1:8080"},
			},
		},
	}
	err = config.ValidateConfig(validCfg)
	assert.NoError(t, err, "合法正则与通配符配置应验证通过")
}

// TestShadowedRouteDetection 测试路由遮蔽检测功能 (REQ-P1-03)
func TestShadowedRouteDetection(t *testing.T) {
	testCases := []struct {
		desc             string
		routes           []config.RouteConfig
		expectShadowWarn bool
		expectedShadow   string
		expectedShadowed string
	}{
		{
			desc: "同 Host 下泛化路由 (prio 90, /api) 完全遮蔽更具体子路径路由 (prio 80, /api/v1)",
			routes: []config.RouteConfig{
				{
					Name: "broad-api",
					Match: config.RouteMatch{
						Type:     "prefix",
						Path:     "/api",
						Priority: 90,
					},
					Target: config.TargetConfig{URL: "http://upstream-broad"},
				},
				{
					Name: "specific-v1",
					Match: config.RouteMatch{
						Type:     "prefix",
						Path:     "/api/v1",
						Priority: 80,
					},
					Target: config.TargetConfig{URL: "http://upstream-v1"},
				},
			},
			expectShadowWarn: true,
			expectedShadow:   "broad-api",
			expectedShadowed: "specific-v1",
		},
		{
			desc: "完全重复的 Exact 路由先定义的遮蔽后续定义的",
			routes: []config.RouteConfig{
				{
					Name: "duplicate-route-1",
					Match: config.RouteMatch{
						Type:     "exact",
						Path:     "/login",
						Priority: 100,
					},
					Target: config.TargetConfig{URL: "http://upstream-1"},
				},
				{
					Name: "duplicate-route-2",
					Match: config.RouteMatch{
						Type:     "exact",
						Path:     "/login",
						Priority: 100,
					},
					Target: config.TargetConfig{URL: "http://upstream-2"},
				},
			},
			expectShadowWarn: true,
			expectedShadow:   "duplicate-route-1",
			expectedShadowed: "duplicate-route-2",
		},
		{
			desc: "跨 Host 优先级倒置场景 (/api prio 90 vs v1.api.example.com prio 80)",
			routes: []config.RouteConfig{
				{
					Name: "api-service",
					Match: config.RouteMatch{
						Type:     "prefix",
						Path:     "/api",
						Priority: 90,
					},
					Target: config.TargetConfig{URL: "http://upstream-api"},
				},
				{
					Name: "v1-api",
					Match: config.RouteMatch{
						Type:     "prefix",
						Path:     "/api",
						Host:     "v1.api.example.com",
						Priority: 80,
					},
					Target: config.TargetConfig{URL: "http://upstream-v1"},
				},
			},
			expectShadowWarn: true,
			expectedShadow:   "api-service",
			expectedShadowed: "v1-api",
		},
		{
			desc: "更深前缀路由拥有更高优先级，不产生遮蔽 (/api/v1 prio 100 > /api prio 90)",
			routes: []config.RouteConfig{
				{
					Name: "broad-api",
					Match: config.RouteMatch{
						Type:     "prefix",
						Path:     "/api",
						Priority: 90,
					},
					Target: config.TargetConfig{URL: "http://upstream-broad"},
				},
				{
					Name: "specific-v1",
					Match: config.RouteMatch{
						Type:     "prefix",
						Path:     "/api/v1",
						Priority: 100, // 更高优先级保护其不被遮蔽
					},
					Target: config.TargetConfig{URL: "http://upstream-v1"},
				},
			},
			expectShadowWarn: false,
		},
		{
			desc: "不同 HTTP 方法之间不产生遮蔽 (GET /items vs POST /items)",
			routes: []config.RouteConfig{
				{
					Name: "get-items",
					Match: config.RouteMatch{
						Type:     "exact",
						Path:     "/items",
						Method:   "GET",
						Priority: 100,
					},
					Target: config.TargetConfig{URL: "http://upstream-get"},
				},
				{
					Name: "post-items",
					Match: config.RouteMatch{
						Type:     "exact",
						Path:     "/items",
						Method:   "POST",
						Priority: 100,
					},
					Target: config.TargetConfig{URL: "http://upstream-post"},
				},
			},
			expectShadowWarn: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			warnings := config.DetectShadowedRoutes(tc.routes)
			if tc.expectShadowWarn {
				require.NotEmpty(t, warnings, "应检测到遮蔽告警")
				found := false
				for _, w := range warnings {
					if w.ShadowRoute == tc.expectedShadow && w.ShadowedRoute == tc.expectedShadowed {
						found = true
						assert.NotEmpty(t, w.Reason)
						break
					}
				}
				assert.True(t, found, "未找到预期的遮蔽对: %s -> %s (实际: %v)", tc.expectedShadow, tc.expectedShadowed, warnings)
			} else {
				assert.Empty(t, warnings, "不应产生遮蔽告警")
			}
		})
	}
}

// TestIntegration_HostSpecificRouting 集成测试：验证 v1.api.example.com/api/* 命中专有上游 (REQ-P1-03)
func TestIntegration_HostSpecificRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 1. 启动两个模拟上游服务
	hostSpecificServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Type", "host-specific")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "host-specific: path=%s, host=%s", r.URL.Path, r.Host)
	}))
	defer hostSpecificServer.Close()

	genericApiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Type", "generic")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "generic: path=%s, host=%s", r.URL.Path, r.Host)
	}))
	defer genericApiServer.Close()

	// 2. 构造符合修正后示例规则的路由配置
	cfg := &config.Config{
		Server: config.ServerConfig{
			Port: 8080,
		},
		Routes: []config.RouteConfig{
			// 专有 Host 路由: v1.api.example.com /api (priority 90)
			{
				Name: "v1-api",
				Match: config.RouteMatch{
					Type:     "prefix",
					Path:     "/api",
					Host:     "v1.api.example.com",
					Priority: 90,
				},
				Target: config.TargetConfig{
					URL: hostSpecificServer.URL,
				},
			},
			// 通用 API 服务路由: 无 Host 限制 (priority 80)
			{
				Name: "api-service",
				Match: config.RouteMatch{
					Type:     "prefix",
					Path:     "/api",
					Priority: 80,
				},
				Target: config.TargetConfig{
					URL: genericApiServer.URL,
				},
			},
		},
	}

	mgr := NewManagerWithConfig(cfg)
	require.NotNil(t, mgr)

	// 3. 测试发往 v1.api.example.com/api/* 的请求命中专有 Host 上游
	v1Requests := []string{
		"/api/users",
		"/api/orders/123",
		"/api/products/list",
	}

	for _, reqPath := range v1Requests {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		req, err := http.NewRequest("GET", reqPath, nil)
		require.NoError(t, err)
		req.Host = "v1.api.example.com"
		c.Request = req

		matched, err := mgr.MatchRoute(c)
		require.NoError(t, err, "路径 %s 匹配失败", reqPath)
		assert.Equal(t, "v1-api", matched.Name, "应该命中专有 Host 路由")
		assert.Equal(t, hostSpecificServer.URL, matched.Target.URL, "应该转发到专有 Host 上游")
	}

	// 4. 测试发往其它 Host (或无特定 Host) 的请求命中通用 API 上游
	genericRequests := []string{
		"/api/users",
		"/api/orders/123",
	}

	for _, reqPath := range genericRequests {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		req, err := http.NewRequest("GET", reqPath, nil)
		require.NoError(t, err)
		req.Host = "other.api.com"
		c.Request = req

		matched, err := mgr.MatchRoute(c)
		require.NoError(t, err, "路径 %s 匹配失败", reqPath)
		assert.Equal(t, "api-service", matched.Name, "应该命中通用 API 路由")
		assert.Equal(t, genericApiServer.URL, matched.Target.URL, "应该转发到通用 API 上游")
	}
}
