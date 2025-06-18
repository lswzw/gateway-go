package cors

import (
	"fmt"
	"gateway-go/internal/plugin/core"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CorsPlugin CORS插件
type CorsPlugin struct {
	*core.BasePlugin
	config map[string]interface{}
}

// New 创建CORS插件
func New() *CorsPlugin {
	return &CorsPlugin{
		BasePlugin: core.NewBasePlugin("cors", 20, nil),
	}
}

// Init 初始化插件
func (p *CorsPlugin) Init(config interface{}) error {
	// 类型断言
	configMap, ok := config.(map[string]interface{})
	if !ok {
		return fmt.Errorf("配置类型错误，期望 map[string]interface{}")
	}

	p.config = configMap
	return nil
}

// Execute 执行插件
func (p *CorsPlugin) Execute(ctx *gin.Context) error {
	// 处理预检请求
	if ctx.Request.Method == "OPTIONS" {
		p.handlePreflight(ctx)
		ctx.Abort()
		return nil
	}

	// 处理实际请求
	p.handleActualRequest(ctx)
	return nil
}

// handlePreflight 处理预检请求
func (p *CorsPlugin) handlePreflight(ctx *gin.Context) {
	origin := ctx.GetHeader("Origin")
	if !p.isOriginAllowed(origin) {
		ctx.Status(http.StatusForbidden)
		return
	}

	// 设置CORS响应头
	ctx.Header("Access-Control-Allow-Origin", origin)
	ctx.Header("Access-Control-Allow-Methods", p.getAllowedMethods())
	ctx.Header("Access-Control-Allow-Headers", p.getAllowedHeaders())
	ctx.Header("Access-Control-Expose-Headers", p.getExposedHeaders())
	ctx.Header("Access-Control-Max-Age", p.getMaxAge())

	ctx.Status(http.StatusOK)
}

// handleActualRequest 处理实际请求
func (p *CorsPlugin) handleActualRequest(ctx *gin.Context) {
	origin := ctx.GetHeader("Origin")
	if origin != "" && p.isOriginAllowed(origin) {
		ctx.Header("Access-Control-Allow-Origin", origin)
		ctx.Header("Access-Control-Expose-Headers", p.getExposedHeaders())
	}
}

// isOriginAllowed 检查源是否允许
func (p *CorsPlugin) isOriginAllowed(origin string) bool {
	if origin == "" {
		return false
	}

	allowedOrigins := p.getAllowedOrigins()
	if len(allowedOrigins) == 0 {
		return false
	}

	// 如果允许所有源
	if len(allowedOrigins) == 1 && allowedOrigins[0] == "*" {
		return true
	}

	// 检查具体源
	for _, allowedOrigin := range allowedOrigins {
		if allowedOrigin == origin {
			return true
		}
	}

	return false
}

// getAllowedOrigins 获取允许的源
func (p *CorsPlugin) getAllowedOrigins() []string {
	if origins, ok := p.config["allowed_origins"].([]interface{}); ok {
		result := make([]string, len(origins))
		for i, origin := range origins {
			result[i] = origin.(string)
		}
		return result
	}
	return []string{"*"}
}

// getAllowedMethods 获取允许的方法
func (p *CorsPlugin) getAllowedMethods() string {
	if methods, ok := p.config["allowed_methods"].([]interface{}); ok {
		result := make([]string, len(methods))
		for i, method := range methods {
			result[i] = method.(string)
		}
		return strings.Join(result, ", ")
	}
	return "GET, POST, PUT, DELETE, OPTIONS"
}

// getAllowedHeaders 获取允许的请求头
func (p *CorsPlugin) getAllowedHeaders() string {
	if headers, ok := p.config["allowed_headers"].([]interface{}); ok {
		result := make([]string, len(headers))
		for i, header := range headers {
			result[i] = header.(string)
		}
		return strings.Join(result, ", ")
	}
	return "*"
}

// getExposedHeaders 获取暴露的响应头
func (p *CorsPlugin) getExposedHeaders() string {
	if headers, ok := p.config["exposed_headers"].([]interface{}); ok {
		result := make([]string, len(headers))
		for i, header := range headers {
			result[i] = header.(string)
		}
		return strings.Join(result, ", ")
	}
	return "Content-Length"
}

// getMaxAge 获取预检请求的缓存时间
func (p *CorsPlugin) getMaxAge() string {
	if maxAge, ok := p.config["max_age"].(string); ok {
		return maxAge
	}
	return "43200" // 默认12小时
}
