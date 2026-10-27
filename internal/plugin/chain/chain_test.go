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
