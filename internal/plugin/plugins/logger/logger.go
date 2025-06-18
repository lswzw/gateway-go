package logger

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"gateway-go/internal/plugin/core"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// LogEntry 日志条目
type LogEntry struct {
	Fields []zap.Field
	Time   time.Time
}

// LoggerPlugin 日志插件
type LoggerPlugin struct {
	*core.BasePlugin
	config map[string]interface{}
	logger *zap.Logger
	// 采样率 (0-1)
	sampleRate float64
	// 追踪ID生成器
	traceIDGenerator func() string
	// 日志缓冲
	logBuffer chan LogEntry
	// 缓冲大小
	bufferSize int
	// 刷新间隔
	flushInterval time.Duration
	// 停止信号
	stopCh chan struct{}
	// 等待组
	wg sync.WaitGroup
}

// New 创建日志插件
func New() *LoggerPlugin {
	return &LoggerPlugin{
		BasePlugin: core.NewBasePlugin("logger", 1, nil),
	}
}

// Init 初始化插件
func (p *LoggerPlugin) Init(config interface{}) error {
	// 类型断言
	configMap, ok := config.(map[string]interface{})
	if !ok {
		return fmt.Errorf("配置类型错误，期望 map[string]interface{}")
	}

	p.config = configMap

	// 创建日志配置
	zapConfig := zap.NewProductionConfig()
	zapConfig.Level = zap.NewAtomicLevelAt(getLogLevel(p.getConfigString("level", "info")))
	zapConfig.EncoderConfig.TimeKey = "timestamp"
	zapConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	zapConfig.EncoderConfig.EncodeDuration = zapcore.StringDurationEncoder

	// 创建日志实例
	logger, err := zapConfig.Build()
	if err != nil {
		return err
	}

	// 设置采样率
	sampleRate := 1.0
	if rate, ok := configMap["sample_rate"].(float64); ok && rate > 0 && rate <= 1 {
		sampleRate = rate
	}

	// 设置缓冲大小
	bufferSize := 1000
	if size, ok := configMap["buffer_size"].(int); ok && size > 0 {
		bufferSize = size
	}

	// 设置刷新间隔
	flushInterval := 5 * time.Second
	if interval, ok := configMap["flush_interval"].(int); ok && interval > 0 {
		flushInterval = time.Duration(interval) * time.Second
	}

	p.logger = logger
	p.sampleRate = sampleRate
	p.traceIDGenerator = func() string {
		return generateTraceID()
	}
	p.logBuffer = make(chan LogEntry, bufferSize)
	p.bufferSize = bufferSize
	p.flushInterval = flushInterval
	p.stopCh = make(chan struct{})

	// 启动日志处理协程
	p.wg.Add(1)
	go p.processLogs()

	return nil
}

// Execute 执行插件
func (p *LoggerPlugin) Execute(ctx *gin.Context) error {
	// 检查是否需要跳过日志
	skipPaths := p.getConfigStringSlice("skip_paths", []string{})
	for _, path := range skipPaths {
		if ctx.Request.URL.Path == path {
			return nil
		}
	}

	// 生成追踪ID
	traceID := p.traceIDGenerator()
	ctx.Set("trace_id", traceID)

	// 开始时间
	start := time.Now()

	// 处理请求
	ctx.Next()

	// 结束时间
	end := time.Now()
	latency := end.Sub(start)

	// 采样检查
	if !p.shouldSample() {
		return nil
	}

	// 构建日志字段
	fields := []zap.Field{
		zap.String("trace_id", traceID),
		zap.String("method", ctx.Request.Method),
		zap.String("path", ctx.Request.URL.Path),
		zap.String("ip", ctx.ClientIP()),
		zap.Int("status", ctx.Writer.Status()),
		zap.Duration("latency", latency),
		zap.String("user_agent", ctx.Request.UserAgent()),
	}

	// 添加请求头
	if p.getConfigBool("log_headers", true) {
		headers := make(map[string]string)
		for k, v := range ctx.Request.Header {
			if len(v) > 0 {
				headers[k] = v[0]
			}
		}
		fields = append(fields, zap.Any("headers", headers))
	}

	// 添加查询参数
	if p.getConfigBool("log_query", true) {
		fields = append(fields, zap.Any("query", ctx.Request.URL.Query()))
	}

	// 添加请求体
	if p.getConfigBool("log_body", false) && ctx.Request.Body != nil {
		body, _ := ctx.GetRawData()
		if len(body) > 0 {
			fields = append(fields, zap.ByteString("body", body))
		}
	}

	// 发送日志到缓冲
	select {
	case p.logBuffer <- LogEntry{
		Fields: fields,
		Time:   time.Now(),
	}:
	default:
		// 缓冲已满，直接写入
		p.logger.Info("request", fields...)
	}

	return nil
}

// Stop 停止插件
func (p *LoggerPlugin) Stop() error {
	close(p.stopCh)
	p.wg.Wait()
	if p.logger != nil {
		p.logger.Sync()
	}
	return nil
}

// processLogs 处理日志
func (p *LoggerPlugin) processLogs() {
	defer p.wg.Done()

	ticker := time.NewTicker(p.flushInterval)
	defer ticker.Stop()

	var entries []LogEntry

	for {
		select {
		case entry := <-p.logBuffer:
			entries = append(entries, entry)
			if len(entries) >= p.bufferSize {
				p.flushLogs(entries)
				entries = entries[:0]
			}
		case <-ticker.C:
			if len(entries) > 0 {
				p.flushLogs(entries)
				entries = entries[:0]
			}
		case <-p.stopCh:
			if len(entries) > 0 {
				p.flushLogs(entries)
			}
			return
		}
	}
}

// flushLogs 刷新日志
func (p *LoggerPlugin) flushLogs(entries []LogEntry) {
	for _, entry := range entries {
		p.logger.Info("request", entry.Fields...)
	}
}

// shouldSample 判断是否需要采样
func (p *LoggerPlugin) shouldSample() bool {
	if p.sampleRate >= 1.0 {
		return true
	}
	return rand.Float64() <= p.sampleRate
}

// generateTraceID 生成追踪ID
func generateTraceID() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 32
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// getLogLevel 获取日志级别
func getLogLevel(level string) zapcore.Level {
	switch level {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// getConfigString 获取字符串配置
func (p *LoggerPlugin) getConfigString(key, defaultValue string) string {
	if value, ok := p.config[key].(string); ok {
		return value
	}
	return defaultValue
}

// getConfigBool 获取布尔配置
func (p *LoggerPlugin) getConfigBool(key string, defaultValue bool) bool {
	if value, ok := p.config[key].(bool); ok {
		return value
	}
	return defaultValue
}

// getConfigStringSlice 获取字符串切片配置
func (p *LoggerPlugin) getConfigStringSlice(key string, defaultValue []string) []string {
	if value, ok := p.config[key].([]interface{}); ok {
		result := make([]string, len(value))
		for i, v := range value {
			if str, ok := v.(string); ok {
				result[i] = str
			}
		}
		return result
	}
	return defaultValue
}
