package testutil

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMockUpstream_Default(t *testing.T) {
	upstream := NewMockUpstream(nil)
	defer upstream.Close()

	if upstream.URL == "" || upstream.ParsedURL == nil {
		t.Fatalf("expected valid URL and ParsedURL, got %s, %v", upstream.URL, upstream.ParsedURL)
	}

	resp, err := http.Get(upstream.URL + "/health")
	if err != nil {
		t.Fatalf("failed to GET mock upstream: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	AssertJSON(t, resp.Body, map[string]interface{}{
		"status": "ok",
	})

	if upstream.RequestCount() != 1 {
		t.Errorf("expected request count 1, got %d", upstream.RequestCount())
	}
	if upstream.LastRequest() == nil {
		t.Fatalf("expected non-nil last request")
	}
	if upstream.LastRequest().URL.Path != "/health" {
		t.Errorf("expected /health, got %s", upstream.LastRequest().URL.Path)
	}
	if len(upstream.Requests()) != 1 {
		t.Errorf("expected 1 request in Requests(), got %d", len(upstream.Requests()))
	}
}

func TestMockUpstream_CustomHandler(t *testing.T) {
	customHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"item_id": 42, "name": "test_item"}`))
	})

	upstream := NewMockUpstream(customHandler)
	defer upstream.Close()

	body := strings.NewReader(`{"action":"create"}`)
	resp, err := http.Post(upstream.URL+"/items", "application/json", body)
	if err != nil {
		t.Fatalf("failed to POST mock upstream: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}

	AssertJSON(t, resp.Body, map[string]interface{}{
		"item_id": 42,
		"name":    "test_item",
	})

	if string(upstream.LastBody()) != `{"action":"create"}` {
		t.Errorf("expected last body to match, got %s", string(upstream.LastBody()))
	}
}

func TestAssertJSON_Types(t *testing.T) {
	jsonBytes := []byte(`{"code": 200, "message": "success", "nested": {"key": "val"}, "list": [1, 2]}`)

	// []byte
	AssertJSON(t, jsonBytes, map[string]interface{}{
		"code":    200,
		"message": "success",
		"nested":  map[string]interface{}{"key": "val"},
		"list":    []interface{}{1, 2},
	})

	// string
	AssertJSON(t, string(jsonBytes), map[string]interface{}{
		"code": 200,
	})

	// io.Reader
	AssertJSON(t, bytes.NewReader(jsonBytes), map[string]interface{}{
		"message": "success",
	})

	// Exact match
	exactJSON := `{"a": 1, "b": "str"}`
	AssertJSONExact(t, exactJSON, map[string]interface{}{
		"a": 1,
		"b": "str",
	})
}

func TestPerformRequestAndContext(t *testing.T) {
	w := httptest.NewRecorder()
	c, engine := NewTestContext(w)

	engine.GET("/ping", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"pong": true})
	})

	if c == nil || engine == nil {
		t.Fatalf("expected non-nil context and engine")
	}

	rec := PerformRequest(engine, "GET", "/ping", nil, map[string]string{
		"X-Custom-Header": "custom-val",
	})

	AssertStatus(t, rec, http.StatusOK)
	AssertJSON(t, rec.Body, map[string]interface{}{
		"pong": true,
	})
}

func TestCreateTempConfigFile(t *testing.T) {
	yamlData := `
server:
  port: 8080
  mode: "test"
`
	filePath := CreateTempConfigFile(t, yamlData)
	if filePath == "" {
		t.Fatalf("expected non-empty file path")
	}
}
