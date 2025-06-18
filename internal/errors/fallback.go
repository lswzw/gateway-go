package errors

import (
	"context"
	"sync"
	"time"
)

// FallbackConfig 降级配置
type FallbackConfig struct {
	// 是否启用降级
	Enabled bool
	// 降级阈值（错误率）
	Threshold float64
	// 降级窗口大小
	WindowSize time.Duration
	// 降级恢复时间
	RecoveryTime time.Duration
	// 降级后的默认响应
	DefaultResponse interface{}
}

// DefaultFallbackConfig 默认降级配置
var DefaultFallbackConfig = FallbackConfig{
	Enabled:         true,
	Threshold:       0.5, // 50% 错误率触发降级
	WindowSize:      time.Minute,
	RecoveryTime:    5 * time.Minute,
	DefaultResponse: nil,
}

// FallbackHandler 降级处理器
type FallbackHandler struct {
	config     FallbackConfig
	errors     []error
	mu         sync.RWMutex
	lastError  time.Time
	isDegraded bool
}

// NewFallbackHandler 创建降级处理器
func NewFallbackHandler(config FallbackConfig) *FallbackHandler {
	return &FallbackHandler{
		config: config,
		errors: make([]error, 0),
	}
}

// RecordError 记录错误
func (h *FallbackHandler) RecordError(err error) {
	if !h.config.Enabled {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// 清理过期错误
	now := time.Now()
	validErrors := make([]error, 0)
	for _, e := range h.errors {
		if now.Sub(h.lastError) <= h.config.WindowSize {
			validErrors = append(validErrors, e)
		}
	}
	h.errors = validErrors

	// 添加新错误
	h.errors = append(h.errors, err)
	h.lastError = now

	// 检查是否需要降级
	if len(h.errors) > 0 {
		errorRate := float64(len(h.errors)) / float64(h.config.WindowSize.Seconds())
		if errorRate >= h.config.Threshold {
			h.isDegraded = true
		}
	}
}

// IsDegraded 检查是否已降级
func (h *FallbackHandler) IsDegraded() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if !h.isDegraded {
		return false
	}

	// 检查是否可以恢复
	if time.Since(h.lastError) >= h.config.RecoveryTime {
		h.mu.Lock()
		h.isDegraded = false
		h.errors = make([]error, 0)
		h.mu.Unlock()
		return false
	}

	return true
}

// Execute 执行带降级的操作
func (h *FallbackHandler) Execute(ctx context.Context, fn func() (interface{}, error)) (interface{}, error) {
	// 检查是否已降级
	if h.IsDegraded() {
		return h.config.DefaultResponse, nil
	}

	// 执行操作
	result, err := fn()
	if err != nil {
		h.RecordError(err)
	}

	return result, err
}

// Reset 重置降级状态
func (h *FallbackHandler) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.errors = make([]error, 0)
	h.isDegraded = false
}

// GetErrorRate 获取当前错误率
func (h *FallbackHandler) GetErrorRate() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.errors) == 0 {
		return 0
	}

	return float64(len(h.errors)) / float64(h.config.WindowSize.Seconds())
}

// SetConfig 更新配置
func (h *FallbackHandler) SetConfig(config FallbackConfig) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.config = config
}
