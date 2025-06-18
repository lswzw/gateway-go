package errors

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrorMessages 错误消息映射
type ErrorMessages struct {
	Messages map[string]map[ErrorCode]string
	mu       sync.RWMutex
}

var (
	// 默认错误消息
	defaultMessages = map[ErrorCode]string{
		ErrInternalServer:     "服务器内部错误",
		ErrConfigLoad:         "配置加载失败",
		ErrConfigValidate:     "配置验证失败",
		"SERVICE_UNAVAILABLE": "服务暂时不可用",
		ErrTimeout:            "请求超时",
		"UNAUTHORIZED":        "未授权访问",
		"FORBIDDEN":           "禁止访问",
		ErrTokenExpired:       "令牌已过期",
		ErrTokenInvalid:       "无效的令牌",
		ErrTooManyRequests:    "请求过于频繁",
		ErrCircuitBreakerOpen: "服务熔断已开启",
		"BAD_REQUEST":         "无效的请求",
		ErrInvalidParam:       "无效的参数",
		ErrMissingParam:       "缺少必要参数",
		ErrInvalidFormat:      "无效的数据格式",
		ErrBusiness:           "业务处理错误",
		ErrResourceNotFound:   "资源不存在",
		ErrResourceExists:     "资源已存在",
		ErrOperationFailed:    "操作失败",
	}

	// 全局错误消息实例
	globalMessages = &ErrorMessages{
		Messages: make(map[string]map[ErrorCode]string),
	}
)

// LoadErrorMessages 加载错误消息
func LoadErrorMessages(lang string, filePath string) error {
	// 读取错误消息文件
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("读取错误消息文件失败: %v", err)
	}

	// 解析错误消息
	var messages map[ErrorCode]string
	if err := json.Unmarshal(data, &messages); err != nil {
		return fmt.Errorf("解析错误消息失败: %v", err)
	}

	// 更新错误消息
	globalMessages.mu.Lock()
	globalMessages.Messages[lang] = messages
	globalMessages.mu.Unlock()

	return nil
}

// LoadAllErrorMessages 加载所有语言的错误消息
func LoadAllErrorMessages(dir string) error {
	// 遍历目录下的所有错误消息文件
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 只处理 JSON 文件
		if !info.IsDir() && filepath.Ext(path) == ".json" {
			// 从文件名获取语言代码
			lang := filepath.Base(path[:len(path)-5])
			return LoadErrorMessages(lang, path)
		}

		return nil
	})
}

// GetErrorMessage 获取错误消息
func GetErrorMessage(code ErrorCode, lang string) string {
	globalMessages.mu.RLock()
	defer globalMessages.mu.RUnlock()

	// 尝试获取指定语言的错误消息
	if messages, ok := globalMessages.Messages[lang]; ok {
		if msg, ok := messages[code]; ok {
			return msg
		}
	}

	// 返回默认错误消息
	if msg, ok := defaultMessages[code]; ok {
		return msg
	}

	// 返回通用错误消息
	return "未知错误"
}
