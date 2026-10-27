package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gateway-go/internal/config"
	"gateway-go/internal/logger"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func setLoggerLevel(t *testing.T, lvl zapcore.Level) {
	t.Helper()
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(io.Discard),
		lvl,
	)
	logger.Log = zap.New(core)
}

// TestBodyStatsNeeded 验证 body 统计只在日志真的会输出时才采集
func TestBodyStatsNeeded(t *testing.T) {
	prev := logger.Log
	defer func() { logger.Log = prev }()

	logger.Log = nil
	if bodyStatsNeeded(&config.Config{}) {
		t.Error("logger 未初始化时不应采集 body 统计")
	}

	setLoggerLevel(t, zapcore.ErrorLevel)
	if bodyStatsNeeded(&config.Config{}) {
		t.Error("log.level=error 时 access log 不输出，不应采集")
	}

	setLoggerLevel(t, zapcore.DebugLevel)
	if !bodyStatsNeeded(&config.Config{}) {
		t.Error("debug 级别下 debug 日志会消费 body 字段，应采集")
	}

	setLoggerLevel(t, zapcore.InfoLevel)
	disabled := false
	cfg := &config.Config{}
	cfg.Log.Access = &disabled
	if bodyStatsNeeded(cfg) {
		t.Error("access log 显式关闭时不应采集")
	}

	cfg.Log.Access = nil
	if !bodyStatsNeeded(cfg) {
		t.Error("info 级别且 access log 开启时应采集")
	}
}

// TestPprofEndpointGated 性能剖析端点默认关闭，仅在 GATEWAY_PPROF=1 时开放
func TestPprofEndpointGated(t *testing.T) {
	t.Setenv("GATEWAY_PPROF", "")
	handler := newAdminHandler(func() bool { return true })

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("默认应返回 404，实际 %d", rec.Code)
	}

	t.Setenv("GATEWAY_PPROF", "1")
	handler = newAdminHandler(func() bool { return true })

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("开启后应返回 200，实际 %d", rec.Code)
	}
}
