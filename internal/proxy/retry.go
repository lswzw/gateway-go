package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
)

// IsIdempotentMethod 判断 HTTP 方法是否为幂等方法
// 根据 RFC 规范与 REQ-P1-01 要求：
// 仅重试幂等方法：GET, HEAD, PUT, DELETE, OPTIONS
// 严禁重试非幂等方法：POST, PATCH 等
func IsIdempotentMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}

// IsRetryableStatus 判断 HTTP 响应状态码是否属于可重试上游错误
// 仅当上游返回 5xx 错误 (502, 503, 504, 500 等) 时触发重试
func IsRetryableStatus(code int) bool {
	return code >= 500 && code <= 599
}

// IsRetryableNetworkError 判断网络传输错误是否可安全重试 (连接拒绝、连接重置、EOF等)
// 注意：context 超时/取消绝对不可重试
func IsRetryableNetworkError(err error) bool {
	if err == nil {
		return false
	}

	// 上下文超时或取消严禁重试
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// 检查网络层错误
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Timeout() {
			if errors.Is(opErr.Err, context.DeadlineExceeded) || errors.Is(opErr.Err, context.Canceled) {
				return false
			}
		}
		var sysErr *os.SyscallError
		if errors.As(opErr.Err, &sysErr) {
			if errors.Is(sysErr.Err, syscall.ECONNREFUSED) ||
				errors.Is(sysErr.Err, syscall.ECONNRESET) ||
				errors.Is(sysErr.Err, syscall.EPIPE) {
				return true
			}
		}
	}

	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	errMsg := strings.ToLower(err.Error())
	if strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "broken pipe") ||
		strings.Contains(errMsg, "server closed connection") ||
		strings.Contains(errMsg, "eof") {
		return true
	}

	return false
}

// IsTimeoutError 判断错误是否为超时错误 (用于网关返回 504 Gateway Timeout)
func IsTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	errMsg := strings.ToLower(err.Error())
	if strings.Contains(errMsg, "deadline exceeded") ||
		strings.Contains(errMsg, "timeout awaiting response headers") ||
		strings.Contains(errMsg, "client.timeout exceeded") {
		return true
	}
	return false
}

// PrepareReplayableBody 为幂等重试请求准备可重放请求体
// 若为幂等方法且存在 Body，缓冲前 maxBufferBytes (默认 64KB)
// 若 Body <= maxBufferBytes，成功缓冲并设置 req.GetBody 以支持重放
// 若 Body > maxBufferBytes，流式拼接还原且不可重试 (req.GetBody 为 nil)
func PrepareReplayableBody(req *http.Request, maxBufferBytes int64) (replayable bool, err error) {
	if req == nil || req.Body == nil || req.Body == http.NoBody {
		return true, nil
	}
	if !IsIdempotentMethod(req.Method) {
		return false, nil
	}

	limitReader := io.LimitReader(req.Body, maxBufferBytes+1)
	buf, err := io.ReadAll(limitReader)
	if err != nil {
		return false, err
	}

	if int64(len(buf)) <= maxBufferBytes {
		bodyBytes := buf
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyBytes)), nil
		}
		return true, nil
	}

	// 超过最大重试缓冲上限，流式拼接还原，标记不可重试
	req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), req.Body))
	req.GetBody = nil
	return false, nil
}

// RetryTransport 自定义 http.RoundTripper 包装器，提供幂等安全重试功能
type RetryTransport struct {
	Base       http.RoundTripper
	MaxRetries int
	OnRetry    func(req *http.Request, attempt int, err error, resp *http.Response)
}

// RoundTrip 执行 HTTP 转发并在满足条件时进行幂等重试
func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	maxRetries := t.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	// 仅重试幂等方法
	canRetry := maxRetries > 0 && IsIdempotentMethod(req.Method)
	// 若存在请求体但无法通过 GetBody 重放，则禁止重试
	if canRetry && req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		canRetry = false
	}

	if !canRetry {
		maxRetries = 0
	}

	var resp *http.Response
	var err error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// 重试前重置请求体并检查 Context
		if attempt > 0 {
			if ctxErr := req.Context().Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if req.GetBody != nil {
				newBody, bodyErr := req.GetBody()
				if bodyErr != nil {
					return resp, err
				}
				req.Body = newBody
			}
		}

		resp, err = base.RoundTrip(req)

		// 检查是否应当重试
		if attempt < maxRetries {
			// 若当前请求 Context 已超时或取消，立即停止重试
			if req.Context().Err() != nil {
				return resp, err
			}

			// 检查是否为可重试的网络传输错误
			if err != nil {
				if IsRetryableNetworkError(err) {
					if t.OnRetry != nil {
						t.OnRetry(req, attempt+1, err, nil)
					}
					if resp != nil && resp.Body != nil {
						_ = resp.Body.Close()
					}
					continue
				}
				// 不可重试的网络错误 (如 context canceled)
				return resp, err
			}

			// 检查是否为可重试的 5xx 状态码
			if resp != nil && IsRetryableStatus(resp.StatusCode) {
				if t.OnRetry != nil {
					t.OnRetry(req, attempt+1, nil, resp)
				}
				// 耗尽并关闭响应体以便底层 TCP 连接能够归还连接池复用
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
				continue
			}
		}

		// 成功、不可重试错误或重试次数耗尽，直接返回
		return resp, err
	}

	return resp, err
}
