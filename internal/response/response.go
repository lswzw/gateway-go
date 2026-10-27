package response

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// ErrorResponse 统一错误响应结构
type ErrorResponse struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// GenerateRequestID 生成唯一的 Request ID (UUID v4 格式)
func GenerateRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	// RFC 4122 UUID v4
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RequestIDMiddleware 注入或透传 Request ID 中间件 (REQ-P1-04)
//
// 契约规则：
// 1. 若请求已包含 X-Request-ID (或 x-request-id / X-Request-Id)，直接保留使用；否则生成新的唯一 ID。
// 2. 写入响应头: c.Writer.Header().Set("X-Request-ID", reqID)
// 3. 存入 gin.Context: c.Set("request_id", reqID)
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = c.GetHeader("x-request-id")
		}
		if reqID == "" {
			reqID = c.GetHeader("X-Request-Id")
		}
		if reqID == "" {
			reqID = GenerateRequestID()
		}

		c.Set("request_id", reqID)
		c.Writer.Header().Set("X-Request-ID", reqID)

		c.Next()
	}
}

// GetRequestID 从 gin.Context 或 HTTP 请求头获取 Request ID
func GetRequestID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if reqID, exists := c.Get("request_id"); exists {
		if s, ok := reqID.(string); ok && s != "" {
			return s
		}
	}
	if reqID := c.GetHeader("X-Request-ID"); reqID != "" {
		return reqID
	}
	if reqID := c.GetHeader("x-request-id"); reqID != "" {
		return reqID
	}
	if reqID := c.GetHeader("X-Request-Id"); reqID != "" {
		return reqID
	}
	return ""
}

// RespondError 统一写出错误响应
//
// 契约规则（REQ-P0-03, REQ-P1-04）：
// 1. 若 c.Writer.Written() 为 true，直接返回，避免二次写出导致报文损坏与状态码错位
// 2. 设置 Content-Type 为 application/json
// 3. 输出统一 JSON 结构体 {"code": code, "message": message, "request_id": requestID}
// 4. 设置与实际响应体一致的 Content-Length 响应头
// 5. 设置 X-Request-ID 响应头
// 6. 调用 c.Abort() 阻止后续处理器继续执行
func RespondError(c *gin.Context, code int, message string, requestID string) {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return
	}
	if requestID == "" {
		requestID = GetRequestID(c)
	}

	resp := ErrorResponse{
		Code:      code,
		Message:   message,
		RequestID: requestID,
	}
	body, err := json.Marshal(resp)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Header("Content-Type", "application/json")
	c.Header("Content-Length", strconv.Itoa(len(body)))
	if requestID != "" {
		c.Header("X-Request-ID", requestID)
	}
	c.Status(code)
	_, _ = c.Writer.Write(body)
	c.Abort()
}
