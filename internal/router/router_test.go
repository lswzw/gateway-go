package router

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"gateway-go/internal/testutil"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestTrieRouter(t *testing.T) {
	tr := NewTrieRouter()

	r1 := &RouteDefinition{Name: "users", Target: TargetService{URL: "http://upstream-users"}}
	r2 := &RouteDefinition{Name: "user-profile", Target: TargetService{URL: "http://upstream-profile"}}

	tr.Insert("/api/v1/users", r1)
	tr.Insert("/api/v1/users/profile", r2)

	// Exact match
	matched, ok := tr.Match("/api/v1/users")
	if !ok || matched.Name != "users" {
		t.Fatalf("expected to match 'users', got ok=%v, matched=%v", ok, matched)
	}

	matched, ok = tr.Match("/api/v1/users/profile")
	if !ok || matched.Name != "user-profile" {
		t.Fatalf("expected to match 'user-profile', got ok=%v, matched=%v", ok, matched)
	}

	// Non-matching path
	_, ok = tr.Match("/api/v1/nonexistent")
	if ok {
		t.Fatalf("expected no match for /api/v1/nonexistent")
	}
}

func TestRouteCache(t *testing.T) {
	cache := NewRouteCache(2)

	r1 := &RouteDefinition{Name: "r1"}
	r2 := &RouteDefinition{Name: "r2"}
	r3 := &RouteDefinition{Name: "r3"}

	cache.Set("/r1", r1)
	cache.Set("/r2", r2)

	got, ok := cache.Get("/r1")
	if !ok || got.Name != "r1" {
		t.Fatalf("expected to get /r1, got %v, %v", got, ok)
	}

	// Setting third item should evict an existing item (LRU policy)
	cache.Set("/r3", r3)

	got, ok = cache.Get("/r3")
	if !ok || got.Name != "r3" {
		t.Fatalf("expected to get /r3, got %v, %v", got, ok)
	}
}

func TestManager_MatchRouteTypesAndPriority(t *testing.T) {
	routes := []RouteDefinition{
		{
			Name: "prefix-route",
			Match: RouteMatch{
				Type:     MatchPrefix,
				Path:     "/api/items",
				Priority: 10,
			},
			Target: TargetService{URL: "http://items-prefix"},
		},
		{
			Name: "exact-route-high-prio",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/api/items/special",
				Priority: 100,
			},
			Target: TargetService{URL: "http://items-special"},
		},
		{
			Name: "regex-route",
			Match: RouteMatch{
				Type:     MatchRegex,
				Regex:    `^/api/order/[0-9]+$`,
				Priority: 50,
			},
			Target: TargetService{URL: "http://order-service"},
		},
		{
			Name: "wildcard-route",
			Match: RouteMatch{
				Type:     MatchWildcard,
				Path:     "/api/*/detail",
				Priority: 20,
			},
			Target: TargetService{URL: "http://wildcard-service"},
		},
	}

	m := &Manager{
		config:     &RouterConfig{Routes: routes},
		regexCache: make(map[string]*regexp.Regexp),
		routeCache: NewRouteCache(100),
	}
	m.trieRouter = NewTrieRouter()
	for i := range routes {
		m.trieRouter.Insert(routes[i].Match.Path, &routes[i])
	}

	// 1. Exact match with higher priority wins over prefix
	w := httptest.NewRecorder()
	c, _ := testutil.NewTestContext(w)
	req, _ := http.NewRequest("GET", "/api/items/special", nil)
	c.Request = req

	target, err := m.MatchRoute(c)
	if err != nil {
		t.Fatalf("failed to match route: %v", err)
	}
	if target.URL != "http://items-special" {
		t.Errorf("expected http://items-special, got %s", target.URL)
	}

	// 2. Prefix match
	w = httptest.NewRecorder()
	c, _ = testutil.NewTestContext(w)
	req, _ = http.NewRequest("GET", "/api/items/other", nil)
	c.Request = req

	target, err = m.MatchRoute(c)
	if err != nil {
		t.Fatalf("failed to match route: %v", err)
	}
	if target.URL != "http://items-prefix" {
		t.Errorf("expected http://items-prefix, got %s", target.URL)
	}

	// 3. Regex match
	w = httptest.NewRecorder()
	c, _ = testutil.NewTestContext(w)
	req, _ = http.NewRequest("GET", "/api/order/12345", nil)
	c.Request = req

	target, err = m.MatchRoute(c)
	if err != nil {
		t.Fatalf("failed to match route: %v", err)
	}
	if target.URL != "http://order-service" {
		t.Errorf("expected http://order-service, got %s", target.URL)
	}

	// 4. Wildcard match
	w = httptest.NewRecorder()
	c, _ = testutil.NewTestContext(w)
	req, _ = http.NewRequest("GET", "/api/product/detail", nil)
	c.Request = req

	target, err = m.MatchRoute(c)
	if err != nil {
		t.Fatalf("failed to match route: %v", err)
	}
	if target.URL != "http://wildcard-service" {
		t.Errorf("expected http://wildcard-service, got %s", target.URL)
	}

	// 5. No match
	w = httptest.NewRecorder()
	c, _ = testutil.NewTestContext(w)
	req, _ = http.NewRequest("GET", "/completely/unknown", nil)
	c.Request = req

	_, err = m.MatchRoute(c)
	if err == nil {
		t.Fatalf("expected error for unmatchable route, got nil")
	}
}

func TestManager_MatchRules_HostMethodHeadersQuery(t *testing.T) {
	routes := []RouteDefinition{
		{
			Name: "conditional-route",
			Match: RouteMatch{
				Type:        MatchExact,
				Path:        "/api/check",
				Host:        "api.example.com",
				Method:      "POST",
				Headers:     map[string]string{"X-Secret": "secret-123"},
				QueryParams: map[string]string{"env": "test"},
				Priority:    10,
			},
			Target: TargetService{URL: "http://conditional-target"},
		},
	}

	m := &Manager{
		config:     &RouterConfig{Routes: routes},
		routeCache: NewRouteCache(10),
	}

	// Mismatched Host
	w := httptest.NewRecorder()
	c, _ := testutil.NewTestContext(w)
	req, _ := http.NewRequest("POST", "/api/check?env=test", nil)
	req.Host = "wrong.example.com"
	req.Header.Set("X-Secret", "secret-123")
	c.Request = req
	if m.matchRule(c, routes[0].Match) {
		t.Errorf("expected matchRule to fail due to wrong host")
	}

	// Mismatched Method
	req.Host = "api.example.com"
	req.Method = "GET"
	if m.matchRule(c, routes[0].Match) {
		t.Errorf("expected matchRule to fail due to wrong method")
	}

	// Mismatched Header
	req.Method = "POST"
	req.Header.Set("X-Secret", "wrong-secret")
	if m.matchRule(c, routes[0].Match) {
		t.Errorf("expected matchRule to fail due to wrong header")
	}

	// Mismatched Query Param
	req.Header.Set("X-Secret", "secret-123")
	req, _ = http.NewRequest("POST", "/api/check?env=prod", nil)
	req.Host = "api.example.com"
	req.Header.Set("X-Secret", "secret-123")
	c.Request = req
	if m.matchRule(c, routes[0].Match) {
		t.Errorf("expected matchRule to fail due to wrong query param")
	}

	// All conditions satisfied
	w = httptest.NewRecorder()
	c, _ = testutil.NewTestContext(w)
	req, _ = http.NewRequest("POST", "/api/check?env=test", nil)
	req.Host = "api.example.com"
	req.Header.Set("X-Secret", "secret-123")
	c.Request = req
	if !m.matchRule(c, routes[0].Match) {
		t.Errorf("expected matchRule to succeed when all conditions match")
	}
}

func TestManager_ABTest(t *testing.T) {
	route := &RouteDefinition{
		Name: "ab-route",
		Target: TargetService{
			URL: "http://default-target",
		},
		Match: RouteMatch{
			Type: MatchExact,
			Path: "/api/feature",
			ABTest: &ABTestConfig{
				Enabled:      true,
				GroupA:       0.5,
				GroupB:       0.5,
				GroupATarget: "http://target-a",
				GroupBTarget: "http://target-b",
			},
		},
	}

	m := &Manager{}

	w := httptest.NewRecorder()
	c, _ := testutil.NewTestContext(w)
	req, _ := http.NewRequest("GET", "/api/feature", nil)
	req.Header.Set("X-Request-ID", "fixed-request-123")
	c.Request = req

	target, err := m.handleABTest(c, route)
	if err != nil {
		t.Fatalf("handleABTest failed: %v", err)
	}

	if target.URL != "http://target-a" && target.URL != "http://target-b" {
		t.Errorf("expected target to be group A or B target, got %s", target.URL)
	}
}

func TestRegisterRoutes(t *testing.T) {
	routes := []RouteDefinition{
		{
			Name: "test-route",
			Match: RouteMatch{
				Type:     MatchExact,
				Path:     "/service/hello",
				Priority: 1,
			},
			Target: TargetService{URL: "http://service-hello"},
		},
	}

	m := &Manager{
		config:     &RouterConfig{Routes: routes},
		routeCache: NewRouteCache(10),
	}

	w := httptest.NewRecorder()
	_, engine := testutil.NewTestContext(w)
	RegisterRoutes(engine, m)

	// 1. Test health check
	rec := testutil.PerformRequest(engine, "GET", "/health", nil, nil)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.AssertJSON(t, rec.Body, map[string]interface{}{"status": "ok"})

	// 2. Test matched route
	rec = testutil.PerformRequest(engine, "GET", "/service/hello", nil, nil)
	testutil.AssertStatus(t, rec, http.StatusOK)
	testutil.AssertJSON(t, rec.Body, map[string]interface{}{
		"message": "路由匹配成功",
	})

	// 3. Test unmatched route
	rec = testutil.PerformRequest(engine, "GET", "/not-found", nil, nil)
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}

func TestManager_LoadFromFileAndReload(t *testing.T) {
	routerYaml := `
routes:
  - name: test-file-route
    match:
      type: exact
      path: /api/test
      priority: 5
    target:
      url: http://target-service
`
	configPath := testutil.CreateTempConfigFile(t, routerYaml)

	mgr, err := NewManager(configPath, nil)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	defer mgr.Close()

	w := httptest.NewRecorder()
	c, _ := testutil.NewTestContext(w)
	req, _ := http.NewRequest("GET", "/api/test", nil)
	c.Request = req

	target, err := mgr.MatchRoute(c)
	if err != nil {
		t.Fatalf("expected route match, got err: %v", err)
	}
	if target.URL != "http://target-service" {
		t.Errorf("expected http://target-service, got %s", target.URL)
	}
}
