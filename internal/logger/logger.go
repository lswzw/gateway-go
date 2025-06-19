package logger

import (
	"gateway-go/internal/config"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	// Log 全局日志实例
	Log *zap.Logger
)

// Init 初始化日志
func Init(config *config.LogConfig) error {
	// 创建日志目录
	if err := os.MkdirAll(filepath.Dir(config.Output), 0755); err != nil {
		return err
	}

	// 配置日志轮转
	writer := &lumberjack.Logger{
		Filename:   config.Output,
		MaxSize:    config.MaxSize,
		MaxBackups: config.MaxBackups,
		MaxAge:     config.MaxAge,
		Compress:   config.Compress,
	}

	// 配置编码器
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// 根据 format 字段选择编码器
	var encoder zapcore.Encoder
	if config.Format == "text" {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	} else {
		// info 级别精简日志：去掉 level、caller、msg 字段
		if config.Level == "info" {
			encoderConfig.LevelKey = ""
			encoderConfig.CallerKey = ""
			encoderConfig.MessageKey = ""
		}
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	}

	// 配置输出
	var writeSyncer zapcore.WriteSyncer
	if config.Output == "stdout" {
		writeSyncer = zapcore.AddSync(os.Stdout)
	} else {
		writeSyncer = zapcore.AddSync(writer)
	}

	// 配置日志级别
	level := zap.InfoLevel
	switch config.Level {
	case "debug":
		level = zap.DebugLevel
	case "info":
		level = zap.InfoLevel
	case "warn":
		level = zap.WarnLevel
	case "error":
		level = zap.ErrorLevel
	}

	// 创建核心
	core := zapcore.NewCore(
		encoder,
		writeSyncer,
		level,
	)

	// 创建日志实例
	Log = zap.New(core, zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel))

	return nil
}

// Debug 记录调试日志
func Debug(msg string, fields ...zap.Field) {
	Log.Debug(msg, fields...)
}

// Info 记录信息日志
func Info(msg string, fields ...zap.Field) {
	Log.Info(msg, fields...)
}

// Warn 记录警告日志
func Warn(msg string, fields ...zap.Field) {
	Log.Warn(msg, fields...)
}

// Error 记录错误日志
func Error(msg string, fields ...zap.Field) {
	Log.Error(msg, fields...)
}

// Fatal 记录致命错误日志
func Fatal(msg string, fields ...zap.Field) {
	Log.Fatal(msg, fields...)
}

// With 创建带有字段的日志记录器
func With(fields ...zap.Field) *zap.Logger {
	return Log.With(fields...)
}

// Sync 同步日志
func Sync() error {
	return Log.Sync()
}

// Field 创建日志字段
func Field(key string, value interface{}) zap.Field {
	switch v := value.(type) {
	case string:
		return zap.String(key, v)
	case int:
		return zap.Int(key, v)
	case int64:
		return zap.Int64(key, v)
	case float64:
		return zap.Float64(key, v)
	case bool:
		return zap.Bool(key, v)
	case time.Time:
		return zap.Time(key, v)
	case time.Duration:
		return zap.Duration(key, v)
	case error:
		return zap.Error(v)
	default:
		return zap.Any(key, v)
	}
}
