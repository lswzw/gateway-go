package chain

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gateway-go/internal/plugin/core"
	"gateway-go/internal/response"

	"github.com/gin-gonic/gin"
)

type mockPlugin struct {
	*core.BasePlugin
	executed bool
	execFn   func(ctx *gin.Context) error
}

func newMockPlugin(name string, order int, execFn func(ctx *gin.Context) error) *mockPlugin {
	return &mockPlugin{
		BasePlugin: core.NewBasePlugin(name, order, nil),
		execFn:     execFn,
	}
}

func (m *mockPlugin) Init(config interface{}) error { return nil }

func (m *mockPlugin) Execute(ctx *gin.Context) error {
	m.executed = true
	if m.execFn != nil {
		return m.execFn(ctx)
	}
	return nil
}

func TestChain_AbortsOnResponseWritten(t *testing.T) {
	c := NewChain()

	p1 := newMockPlugin("p1", 1, func(ctx *gin.Context) error {
		response.RespondError(ctx, http.StatusForbidden, "forbidden", "req-1")
		return nil
	})
	p2 := newMockPlugin("p2", 2, func(ctx *gin.Context) error {
		return nil
	})

	c.AddPlugin(p1)
	c.AddPlugin(p2)

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	ctx.Request = req

	err := c.Execute(ctx)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !p1.executed {
		t.Errorf("expected p1 to be executed")
	}
	if p2.executed {
		t.Errorf("expected p2 NOT to be executed because p1 aborted the context")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestChain_RecordsFailedPluginOnError(t *testing.T) {
	c := NewChain()

	p1 := newMockPlugin("p1_err", 1, func(ctx *gin.Context) error {
		return errors.New("something exploded")
	})
	p2 := newMockPlugin("p2", 2, func(ctx *gin.Context) error {
		return nil
	})

	c.AddPlugin(p1)
	c.AddPlugin(p2)

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	ctx.Request = req

	err := c.Execute(ctx)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	failedPlugin, exists := ctx.Get("failed_plugin")
	if !exists || failedPlugin != "p1_err" {
		t.Errorf("expected failed_plugin to be 'p1_err', got %v", failedPlugin)
	}

	if p2.executed {
		t.Errorf("expected p2 NOT to be executed after p1 returned error")
	}
}

func TestChain_ExecutionOrder(t *testing.T) {
	c := NewChain()
	var executionOrder []string

	p1 := newMockPlugin("p-third", 30, func(ctx *gin.Context) error {
		executionOrder = append(executionOrder, "p-third")
		return nil
	})
	p2 := newMockPlugin("p-first", 10, func(ctx *gin.Context) error {
		executionOrder = append(executionOrder, "p-first")
		return nil
	})
	p3 := newMockPlugin("p-second", 20, func(ctx *gin.Context) error {
		executionOrder = append(executionOrder, "p-second")
		return nil
	})

	c.AddPlugin(p1)
	c.AddPlugin(p2)
	c.AddPlugin(p3)

	if len(c.Plugins()) != 3 {
		t.Fatalf("expected 3 plugins, got %d", len(c.Plugins()))
	}

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	ctx.Request = req

	if err := c.Execute(ctx); err != nil {
		t.Fatalf("execute returned error: %v", err)
	}

	if len(executionOrder) != 3 ||
		executionOrder[0] != "p-first" ||
		executionOrder[1] != "p-second" ||
		executionOrder[2] != "p-third" {
		t.Errorf("expected order [p-first, p-second, p-third], got %v", executionOrder)
	}

	c.Clear()
	if len(c.Plugins()) != 0 {
		t.Errorf("expected 0 plugins after Clear(), got %d", len(c.Plugins()))
	}
}

type mockPluginCache struct {
	store map[string]interface{}
}

func (m *mockPluginCache) Get(key string) (interface{}, bool) {
	v, ok := m.store[key]
	return v, ok
}

func (m *mockPluginCache) Set(key string, result interface{}) {
	m.store[key] = result
}

func TestChain_Caching(t *testing.T) {
	cache := &mockPluginCache{store: make(map[string]interface{})}
	genKey := func(ctx *gin.Context, pluginName string) string {
		return pluginName + ":" + ctx.Request.URL.Path
	}

	c := NewChainWithCache(cache, genKey)

	executedCount := 0
	p := newMockPlugin("cached-plugin", 1, func(ctx *gin.Context) error {
		executedCount++
		ctx.Set("plugin_result_cached-plugin", "cached_val_xyz")
		return nil
	})
	c.AddPlugin(p)

	// 1. First execution - cache miss
	w1 := httptest.NewRecorder()
	ctx1, _ := gin.CreateTestContext(w1)
	req1, _ := http.NewRequest("GET", "/cache-test", nil)
	ctx1.Request = req1

	if err := c.Execute(ctx1); err != nil {
		t.Fatalf("first execution failed: %v", err)
	}
	if executedCount != 1 {
		t.Errorf("expected executedCount 1, got %d", executedCount)
	}

	// 2. Second execution - cache hit
	w2 := httptest.NewRecorder()
	ctx2, _ := gin.CreateTestContext(w2)
	req2, _ := http.NewRequest("GET", "/cache-test", nil)
	ctx2.Request = req2

	if err := c.Execute(ctx2); err != nil {
		t.Fatalf("second execution failed: %v", err)
	}
	if executedCount != 1 {
		t.Errorf("expected executedCount to remain 1 on cache hit, got %d", executedCount)
	}
	val, exists := ctx2.Get("plugin_result_cached-plugin")
	if !exists || val != "cached_val_xyz" {
		t.Errorf("expected cached value 'cached_val_xyz', got %v", val)
	}
}

func TestChain_AlreadyAborted(t *testing.T) {
	c := NewChain()
	p := newMockPlugin("p", 1, func(ctx *gin.Context) error {
		t.Fatal("plugin should not be executed when context is already aborted")
		return nil
	})
	c.AddPlugin(p)

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	ctx.Request = req
	ctx.Abort()

	if err := c.Execute(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

