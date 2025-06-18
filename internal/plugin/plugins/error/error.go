package error

import (
	"context"
	"fmt"
	"gateway-go/internal/errors"
	"gateway-go/internal/plugin/core"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ErrorPlugin 错误处理插件
type ErrorPlugin struct {
	*core.BasePlugin
	config   map[string]interface{}
	logger   *zap.Logger
	notifier *errors.ErrorNotifier
	fallback *errors.FallbackHandler
	retry    errors.RetryConfig
}

// New 创建错误处理插件
func New() *ErrorPlugin {
	return &ErrorPlugin{
		BasePlugin: core.NewBasePlugin("error", 100, nil), // 高优先级，最后执行
	}
}

// Init 初始化插件
func (p *ErrorPlugin) Init(config interface{}) error {
	// 类型断言
	configMap, ok := config.(map[string]interface{})
	if !ok {
		return fmt.Errorf("配置类型错误，期望 map[string]interface{}")
	}

	p.config = configMap

	// 创建错误通知器
	notifier := errors.NewErrorNotifier(errors.DefaultNotificationConfig)

	// 创建降级处理器
	fallback := errors.NewFallbackHandler(errors.DefaultFallbackConfig)

	p.notifier = notifier
	p.fallback = fallback
	p.retry = errors.DefaultRetryConfig

	return nil
}

// Execute 执行插件
func (p *ErrorPlugin) Execute(ctx *gin.Context) error {
	// 设置错误处理中间件
	ctx.Next()

	// 检查是否有错误
	if len(ctx.Errors) > 0 {
		err := ctx.Errors.Last().Err
		p.handleError(ctx, err)
	}

	return nil
}

// handleError 处理错误
func (p *ErrorPlugin) handleError(ctx *gin.Context, err error) {
	// 尝试转换为自定义错误
	if e, ok := errors.As(err); ok {
		// 记录错误日志
		p.logger.Error("请求处理错误",
			zap.Int("code", int(e.Code)),
			zap.String("message", e.Message),
			zap.Any("details", e.Details),
			zap.Error(e.Err),
			zap.String("path", ctx.Request.URL.Path),
			zap.String("method", ctx.Request.Method),
			zap.String("ip", ctx.ClientIP()),
		)

		// 发送错误通知
		notifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p.notifier.Notify(notifyCtx, e)

		// 记录错误用于降级判断
		p.fallback.RecordError(e)

		// 返回错误响应
		ctx.JSON(e.HTTPStatus(), e)
		return
	}

	// 处理未知错误
	p.logger.Error("未知错误",
		zap.Error(err),
		zap.String("path", ctx.Request.URL.Path),
		zap.String("method", ctx.Request.Method),
		zap.String("ip", ctx.ClientIP()),
	)

	// 发送错误通知
	notifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.notifier.Notify(notifyCtx, err)

	// 记录错误用于降级判断
	p.fallback.RecordError(err)

	// 返回通用错误响应
	ctx.JSON(http.StatusInternalServerError, errors.New(errors.ErrInternalServerError, "服务器内部错误"))
}

// SetLogger 设置日志记录器
func (p *ErrorPlugin) SetLogger(logger *zap.Logger) {
	p.logger = logger
}

// AddNotificationChannel 添加通知渠道
func (p *ErrorPlugin) AddNotificationChannel(channel errors.NotificationChannel) {
	p.notifier.AddChannel(channel)
}

// SetRetryConfig 设置重试配置
func (p *ErrorPlugin) SetRetryConfig(config errors.RetryConfig) {
	p.retry = config
}

// SetFallbackConfig 设置降级配置
func (p *ErrorPlugin) SetFallbackConfig(config errors.FallbackConfig) {
	p.fallback.SetConfig(config)
}

// SetNotificationConfig 设置通知配置
func (p *ErrorPlugin) SetNotificationConfig(config errors.NotificationConfig) {
	p.notifier.SetConfig(config)
}
