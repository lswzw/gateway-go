package errors

import (
	"context"
	"fmt"
	"time"
)

// RetryConfig 重试配置
type RetryConfig struct {
	// 最大重试次数
	MaxRetries int
	// 重试间隔
	RetryInterval time.Duration
	// 最大重试间隔
	MaxRetryInterval time.Duration
	// 重试间隔增长因子
	BackoffFactor float64
	// 是否启用指数退避
	EnableBackoff bool
}

// DefaultRetryConfig 默认重试配置
var DefaultRetryConfig = RetryConfig{
	MaxRetries:       3,
	RetryInterval:    time.Second,
	MaxRetryInterval: 30 * time.Second,
	BackoffFactor:    2.0,
	EnableBackoff:    true,
}

// RetryableError 可重试错误
type RetryableError struct {
	Err error
}

func (e *RetryableError) Error() string {
	return fmt.Sprintf("可重试错误: %v", e.Err)
}

// IsRetryable 判断错误是否可重试
func IsRetryable(err error) bool {
	// 检查是否是自定义的可重试错误
	if _, ok := err.(*RetryableError); ok {
		return true
	}

	// 检查是否是系统错误
	if e, ok := err.(*Error); ok {
		switch e.Code {
		case ErrServiceUnavailableCode, // 503
			ErrRequestTimeout,      // 408
			ErrInternalServerError: // 500
			return true
		}
	}

	return false
}

// Retry 执行重试操作
func Retry(ctx context.Context, fn func() error, config RetryConfig) error {
	var lastErr error
	interval := config.RetryInterval

	for i := 0; i <= config.MaxRetries; i++ {
		// 检查上下文是否已取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 执行操作
		err := fn()
		if err == nil {
			return nil
		}

		// 检查是否可重试
		if !IsRetryable(err) {
			return err
		}

		lastErr = err

		// 如果是最后一次重试，直接返回错误
		if i == config.MaxRetries {
			break
		}

		// 计算下一次重试间隔
		if config.EnableBackoff {
			interval = time.Duration(float64(interval) * config.BackoffFactor)
			if interval > config.MaxRetryInterval {
				interval = config.MaxRetryInterval
			}
		}

		// 等待重试
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}

	return fmt.Errorf("重试失败: %v", lastErr)
}

// WithRetry 包装错误为可重试错误
func WithRetry(err error) error {
	return &RetryableError{Err: err}
}
