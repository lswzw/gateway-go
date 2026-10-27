package response

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// ErrorResponse 统一错误响应结构
type ErrorResponse struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
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
	if reqID := c.GetHeader("X-Request-Id"); reqID != "" {
		return reqID
	}
	if reqID := c.GetHeader("X-Request-ID"); reqID != "" {
		return reqID
	}
	return ""
}

// RespondError 统一写出错误响应
//
// 契约规则（REQ-P0-03）：
// 1. 若 c.Writer.Written() 为 true，直接返回，避免二次写出导致报文损坏与状态码错位
// 2. 设置 Content-Type 为 application/json
// 3. 输出统一 JSON 结构体 {"code": code, "message": message, "request_id": requestID}
// 4. 设置与实际响应体一致的 Content-Length 响应头
// 5. 调用 c.Abort() 阻止后续处理器继续执行
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
	c.Status(code)
	_, _ = c.Writer.Write(body)
	c.Abort()
}
