package errors

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// NotificationLevel 通知级别
type NotificationLevel int

const (
	LevelInfo NotificationLevel = iota
	LevelWarning
	LevelError
	LevelCritical
)

// NotificationChannel 通知渠道
type NotificationChannel interface {
	// Send 发送通知
	Send(ctx context.Context, level NotificationLevel, message string, details interface{}) error
}

// NotificationConfig 通知配置
type NotificationConfig struct {
	// 是否启用通知
	Enabled bool
	// 通知级别阈值
	LevelThreshold NotificationLevel
	// 通知间隔
	Interval time.Duration
	// 通知渠道
	Channels []NotificationChannel
}

// DefaultNotificationConfig 默认通知配置
var DefaultNotificationConfig = NotificationConfig{
	Enabled:        true,
	LevelThreshold: LevelError,
	Interval:       time.Minute,
	Channels:       make([]NotificationChannel, 0),
}

// ErrorNotifier 错误通知器
type ErrorNotifier struct {
	config     NotificationConfig
	lastNotify time.Time
	mu         sync.RWMutex
}

// NewErrorNotifier 创建错误通知器
func NewErrorNotifier(config NotificationConfig) *ErrorNotifier {
	return &ErrorNotifier{
		config:     config,
		lastNotify: time.Time{},
	}
}

// Notify 发送通知
func (n *ErrorNotifier) Notify(ctx context.Context, err error) error {
	if !n.config.Enabled {
		return nil
	}

	// 检查通知级别
	level := n.getErrorLevel(err)
	if level < n.config.LevelThreshold {
		return nil
	}

	// 检查通知间隔
	n.mu.Lock()
	if time.Since(n.lastNotify) < n.config.Interval {
		n.mu.Unlock()
		return nil
	}
	n.lastNotify = time.Now()
	n.mu.Unlock()

	// 构建通知消息
	message := n.buildMessage(err)
	details := n.buildDetails(err)

	// 发送通知
	var lastErr error
	for _, channel := range n.config.Channels {
		if err := channel.Send(ctx, level, message, details); err != nil {
			lastErr = err
		}
	}

	return lastErr
}

// getErrorLevel 获取错误级别
func (n *ErrorNotifier) getErrorLevel(err error) NotificationLevel {
	if e, ok := err.(*Error); ok {
		switch e.Code {
		case ErrInternalServerError:
			return LevelCritical
		case ErrServiceUnavailableCode, // 503: 服务不可用或熔断器打开
			ErrRequestTimeout:
			return LevelError
		case 429, // ErrTooManyRequests
			ErrNotFound:
			return LevelWarning
		default:
			return LevelInfo
		}
	}
	return LevelError
}

// buildMessage 构建通知消息
func (n *ErrorNotifier) buildMessage(err error) string {
	if e, ok := err.(*Error); ok {
		return fmt.Sprintf("[%d] %s", e.Code, e.Message)
	}
	return err.Error()
}

// buildDetails 构建通知详情
func (n *ErrorNotifier) buildDetails(err error) interface{} {
	if e, ok := err.(*Error); ok {
		return map[string]interface{}{
			"code":    e.Code,
			"message": e.Message,
			"details": e.Details,
			"error":   e.Err,
		}
	}
	return nil
}

// AddChannel 添加通知渠道
func (n *ErrorNotifier) AddChannel(channel NotificationChannel) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.config.Channels = append(n.config.Channels, channel)
}

// RemoveChannel 移除通知渠道
func (n *ErrorNotifier) RemoveChannel(channel NotificationChannel) {
	n.mu.Lock()
	defer n.mu.Unlock()

	channels := make([]NotificationChannel, 0)
	for _, ch := range n.config.Channels {
		if ch != channel {
			channels = append(channels, ch)
		}
	}
	n.config.Channels = channels
}

// SetConfig 更新配置
func (n *ErrorNotifier) SetConfig(config NotificationConfig) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.config = config
}
