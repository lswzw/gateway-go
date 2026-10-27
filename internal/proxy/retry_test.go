package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestIsIdempotentMethod(t *testing.T) {
	cases := []struct {
		method   string
		expected bool
	}{
		{"GET", true},
		{"get", true},
		{"HEAD", true},
		{"PUT", true},
		{"DELETE", true},
		{"OPTIONS", true},
		{"POST", false},
		{"post", false},
		{"PATCH", false},
		{"patch", false},
		{"CONNECT", false},
		{"TRACE", false},
		{"CUSTOM", false},
	}

	for _, tc := range cases {
		actual := IsIdempotentMethod(tc.method)
		if actual != tc.expected {
			t.Errorf("method %s: expected %v, got %v", tc.method, tc.expected, actual)
		}
	}
}

func TestIsRetryableStatus(t *testing.T) {
	cases := []struct {
		status   int
		expected bool
	}{
		{200, false},
		{201, false},
		{301, false},
		{400, false},
		{401, false},
		{403, false},
		{404, false},
		{500, true},
		{502, true},
		{503, true},
		{504, true},
		{599, true},
	}

	for _, tc := range cases {
		actual := IsRetryableStatus(tc.status)
		if actual != tc.expected {
			t.Errorf("status %d: expected %v, got %v", tc.status, tc.expected, actual)
		}
	}
}

func TestIsRetryableNetworkError(t *testing.T) {
	if IsRetryableNetworkError(nil) {
		t.Error("nil error should not be retryable")
	}
	if IsRetryableNetworkError(context.Canceled) {
		t.Error("context.Canceled should not be retryable")
	}
	if IsRetryableNetworkError(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded should not be retryable")
	}
	if !IsRetryableNetworkError(syscall.ECONNREFUSED) {
		t.Error("ECONNREFUSED should be retryable")
	}
	if !IsRetryableNetworkError(syscall.ECONNRESET) {
		t.Error("ECONNRESET should be retryable")
	}
	if !IsRetryableNetworkError(errors.New("dial tcp: connection refused")) {
		t.Error("connection refused text should be retryable")
	}
	if !IsRetryableNetworkError(io.EOF) {
		t.Error("io.EOF should be retryable")
	}
}

func TestPrepareReplayableBody(t *testing.T) {
	// 1. 小于等于 64KB 的 PUT 请求
	smallData := []byte("hello world small payload")
	req, _ := http.NewRequest(http.MethodPut, "http://example.com", bytes.NewReader(smallData))
	replayable, err := PrepareReplayableBody(req, 64*1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !replayable {
		t.Error("small body should be replayable")
	}
	if req.GetBody == nil {
		t.Fatal("req.GetBody should be set")
	}
	body1, _ := req.GetBody()
	b1, _ := io.ReadAll(body1)
	if string(b1) != string(smallData) {
		t.Errorf("expected %s, got %s", string(smallData), string(b1))
	}

	// 2. 超过 64KB 的 PUT 请求
	largeData := make([]byte, 70*1024) // 70KB
	for i := range largeData {
		largeData[i] = 'A'
	}
	req2, _ := http.NewRequest(http.MethodPut, "http://example.com", bytes.NewReader(largeData))
	replayable2, err := PrepareReplayableBody(req2, 64*1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if replayable2 {
		t.Error("body > 64KB should not be replayable")
	}
	if req2.GetBody != nil {
		t.Error("req.GetBody should be nil for body > 64KB")
	}
	// 验证流式数据并未截断
	allData, err := io.ReadAll(req2.Body)
	if err != nil {
		t.Fatalf("failed reading unreplayable body: %v", err)
	}
	if len(allData) != len(largeData) {
		t.Errorf("expected len %d, got %d", len(largeData), len(allData))
	}

	// 3. POST 请求不缓冲
	req3, _ := http.NewRequest(http.MethodPost, "http://example.com", bytes.NewReader(smallData))
	replayable3, _ := PrepareReplayableBody(req3, 64*1024)
	if replayable3 {
		t.Error("POST request body should not be replayable")
	}

	// 4. 空 Body
	req4, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	replayable4, _ := PrepareReplayableBody(req4, 64*1024)
	if !replayable4 {
		t.Error("nil body should be replayable")
	}
}

type mockRoundTripper struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.fn(req)
}

func TestRetryTransport(t *testing.T) {
	// 测试 1: 503 重试成功
	attempts := 0
	mock := &mockRoundTripper{
		fn: func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts < 3 {
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Body:       io.NopCloser(strings.NewReader("upstream error")),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("success")),
				Header:     make(http.Header),
			}, nil
		},
	}

	rt := &RetryTransport{
		Base:       mock,
		MaxRetries: 2,
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}

	// 测试 2: POST 请求不重试
	attempts = 0
	reqPost, _ := http.NewRequest(http.MethodPost, "http://example.com/test", nil)
	resp, err = rt.RoundTrip(reqPost)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", resp.StatusCode)
	}
	if attempts != 1 {
		t.Errorf("expected exactly 1 attempt for POST, got %d", attempts)
	}

	// 测试 3: Context 超时停止重试
	attempts = 0
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	mockTimeout := &mockRoundTripper{
		fn: func(req *http.Request) (*http.Response, error) {
			attempts++
			time.Sleep(30 * time.Millisecond)
			return nil, context.DeadlineExceeded
		},
	}
	rtTimeout := &RetryTransport{
		Base:       mockTimeout,
		MaxRetries: 3,
	}
	reqTimeout, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com/timeout", nil)
	_, err = rtTimeout.RoundTrip(reqTimeout)
	if err == nil {
		t.Fatal("expected context deadline error")
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt when context expired, got %d", attempts)
	}
}
