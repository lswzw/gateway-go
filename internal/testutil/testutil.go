package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// MockUpstream wraps an httptest.Server with request logging and inspection helpers.
type MockUpstream struct {
	Server       *httptest.Server
	URL          string
	ParsedURL    *url.URL
	handler      http.Handler
	mu           sync.Mutex
	requestCount int
	requests     []*http.Request
	bodies       [][]byte
}

// NewMockUpstream creates and starts a new MockUpstream instance.
// If handler is nil, a default 200 OK JSON handler is used.
func NewMockUpstream(handler http.Handler) *MockUpstream {
	if handler == nil {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		})
	}

	mock := &MockUpstream{
		handler:  handler,
		requests: make([]*http.Request, 0),
		bodies:   make([][]byte, 0),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		mock.mu.Lock()
		mock.requestCount++
		mock.requests = append(mock.requests, r.Clone(r.Context()))
		mock.bodies = append(mock.bodies, bodyBytes)
		mock.mu.Unlock()

		mock.handler.ServeHTTP(w, r)
	}))

	mock.Server = server
	mock.URL = server.URL
	parsed, _ := url.Parse(server.URL)
	mock.ParsedURL = parsed

	return mock
}

// Close shuts down the mock upstream server.
func (m *MockUpstream) Close() {
	if m.Server != nil {
		m.Server.Close()
	}
}

// RequestCount returns the total number of requests received.
func (m *MockUpstream) RequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requestCount
}

// LastRequest returns a clone of the most recent request received, or nil if none.
func (m *MockUpstream) LastRequest() *http.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return nil
	}
	return m.requests[len(m.requests)-1]
}

// LastBody returns the body bytes of the most recent request, or nil if none.
func (m *MockUpstream) LastBody() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.bodies) == 0 {
		return nil
	}
	return m.bodies[len(m.bodies)-1]
}

// Requests returns a copy of all requests received so far.
func (m *MockUpstream) Requests() []*http.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*http.Request, len(m.requests))
	copy(out, m.requests)
	return out
}

// AssertJSON verifies that body (which can be []byte, string, or io.Reader)
// contains all the key-value pairs specified in expectedMap.
func AssertJSON(t testing.TB, body interface{}, expectedMap map[string]interface{}) {
	t.Helper()

	bodyBytes, err := toBytes(body)
	if err != nil {
		t.Fatalf("AssertJSON: failed to read body: %v", err)
	}

	var actualMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &actualMap); err != nil {
		t.Fatalf("AssertJSON: failed to unmarshal JSON: %v (raw: %s)", err, string(bodyBytes))
	}

	for k, expectedVal := range expectedMap {
		actualVal, exists := actualMap[k]
		if !exists {
			t.Errorf("AssertJSON: missing expected key %q in response %s", k, string(bodyBytes))
			continue
		}

		if !compareJSONValues(expectedVal, actualVal) {
			t.Errorf("AssertJSON: key %q mismatch: expected %#v (%T), got %#v (%T)",
				k, expectedVal, expectedVal, actualVal, actualVal)
		}
	}
}

// AssertJSONExact verifies that body matches expectedMap exactly (no missing or extra keys).
func AssertJSONExact(t testing.TB, body interface{}, expectedMap map[string]interface{}) {
	t.Helper()

	bodyBytes, err := toBytes(body)
	if err != nil {
		t.Fatalf("AssertJSONExact: failed to read body: %v", err)
	}

	var actualMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &actualMap); err != nil {
		t.Fatalf("AssertJSONExact: failed to unmarshal JSON: %v (raw: %s)", err, string(bodyBytes))
	}

	if len(actualMap) != len(expectedMap) {
		t.Errorf("AssertJSONExact: key count mismatch: expected %d keys, got %d keys (actual: %s)",
			len(expectedMap), len(actualMap), string(bodyBytes))
	}

	AssertJSON(t, bodyBytes, expectedMap)
}

// AssertStatus verifies that the response recorder has the expected HTTP status code.
func AssertStatus(t testing.TB, rec *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if rec.Code != expected {
		t.Errorf("expected HTTP status %d, got %d (body: %s)", expected, rec.Code, rec.Body.String())
	}
}

// NewTestContext creates a test gin.Context and its engine for unit testing.
func NewTestContext(w *httptest.ResponseRecorder) (*gin.Context, *gin.Engine) {
	c, engine := gin.CreateTestContext(w)
	return c, engine
}

// PerformRequest executes an HTTP request against a handler (such as a *gin.Engine).
func PerformRequest(handler http.Handler, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	req, _ := http.NewRequest(method, path, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// CreateTempConfigFile creates a temporary YAML configuration file and schedules its removal.
func CreateTempConfigFile(t testing.TB, yamlContent string) string {
	t.Helper()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to create temp config file: %v", err)
	}

	return configPath
}

func toBytes(v interface{}) ([]byte, error) {
	switch val := v.(type) {
	case []byte:
		return val, nil
	case string:
		return []byte(val), nil
	case io.Reader:
		return io.ReadAll(val)
	default:
		return json.Marshal(v)
	}
}

func compareJSONValues(expected, actual interface{}) bool {
	if expected == nil && actual == nil {
		return true
	}
	if expected == nil || actual == nil {
		return false
	}

	// Handle number comparisons across float64, int, int64, etc.
	expectedNum, expIsNum := toFloat64(expected)
	actualNum, actIsNum := toFloat64(actual)
	if expIsNum && actIsNum {
		return expectedNum == actualNum
	}

	// Nested map comparison
	if expMap, ok := expected.(map[string]interface{}); ok {
		if actMap, ok := actual.(map[string]interface{}); ok {
			for k, v := range expMap {
				actV, exists := actMap[k]
				if !exists || !compareJSONValues(v, actV) {
					return false
				}
			}
			return true
		}
		return false
	}

	// Nested slice comparison
	if expSlice, ok := expected.([]interface{}); ok {
		if actSlice, ok := actual.([]interface{}); ok {
			if len(expSlice) != len(actSlice) {
				return false
			}
			for i := range expSlice {
				if !compareJSONValues(expSlice[i], actSlice[i]) {
					return false
				}
			}
			return true
		}
		return false
	}

	return reflect.DeepEqual(expected, actual) || fmt.Sprintf("%v", expected) == fmt.Sprintf("%v", actual)
}

func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}
