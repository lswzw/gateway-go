package logger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gateway-go/internal/config"

	"go.uber.org/zap"
)

// TestLoggerInfoLevelRetainsLevelAndMsg 验证 info 级别下保留 standard zap 字段 (level, msg, time, caller) (REQ-P0-05)
func TestLoggerInfoLevelRetainsLevelAndMsg(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test_info.log")

	logCfg := &config.LogConfig{
		Level:      "info",
		Format:     "json",
		Output:     logFile,
		MaxSize:    10,
		MaxAge:     7,
		MaxBackups: 3,
		Compress:   false,
	}

	if err := Init(logCfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	Log.Info("access",
		zap.String("method", "GET"),
		zap.String("path", "/api/v1/users"),
		zap.Int("status", 200),
		zap.Int64("rt_ms", 12),
	)

	_ = Sync()

	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("expected non-empty log output")
	}

	for _, line := range lines {
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not valid JSON: %v, line: %s", err, line)
		}

		if entry["level"] != "info" {
			t.Errorf("expected level 'info', got %v", entry["level"])
		}

		if entry["msg"] != "access" {
			t.Errorf("expected msg 'access', got %v", entry["msg"])
		}

		if _, exists := entry["time"]; !exists {
			t.Errorf("expected time field to exist")
		}

		if entry["method"] != "GET" {
			t.Errorf("expected method 'GET', got %v", entry["method"])
		}

		if entry["path"] != "/api/v1/users" {
			t.Errorf("expected path '/api/v1/users', got %v", entry["path"])
		}

		if entry["status"] != float64(200) {
			t.Errorf("expected status 200, got %v", entry["status"])
		}

		if entry["rt_ms"] != float64(12) {
			t.Errorf("expected rt_ms 12, got %v", entry["rt_ms"])
		}
	}
}
