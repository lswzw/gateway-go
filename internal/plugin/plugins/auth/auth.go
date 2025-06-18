package auth

import (
	"fmt"
	"net/http"

	"gateway-go/internal/plugin/core"

	"github.com/gin-gonic/gin"
)

// AuthPlugin 认证插件
type AuthPlugin struct {
	*core.BasePlugin
	config map[string]interface{}
}

// New 创建认证插件
func New() *AuthPlugin {
	return &AuthPlugin{
		BasePlugin: core.NewBasePlugin("auth", 1, nil),
	}
}

// Init 初始化插件
func (p *AuthPlugin) Init(config interface{}) error {
	// 类型断言
	configMap, ok := config.(map[string]interface{})
	if !ok {
		return fmt.Errorf("配置类型错误，期望 map[string]interface{}")
	}

	p.config = configMap
	return nil
}

// Execute 执行插件
func (p *AuthPlugin) Execute(ctx *gin.Context) error {
	// 从配置中获取认证方式
	authType, ok := p.config["type"].(string)
	if !ok {
		authType = "token" // 默认使用 token 认证
	}

	switch authType {
	case "token":
		return p.handleTokenAuth(ctx)
	case "basic":
		return p.handleBasicAuth(ctx)
	default:
		return fmt.Errorf("不支持的认证方式: %s", authType)
	}
}

// handleTokenAuth 处理 Token 认证
func (p *AuthPlugin) handleTokenAuth(ctx *gin.Context) error {
	token := ctx.GetHeader("Authorization")
	if token == "" {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"error": "未提供认证令牌",
		})
		return fmt.Errorf("未提供认证令牌")
	}

	// TODO: 实现 token 验证逻辑
	return nil
}

// handleBasicAuth 处理 Basic 认证
func (p *AuthPlugin) handleBasicAuth(ctx *gin.Context) error {
	username, password, ok := ctx.Request.BasicAuth()
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"error": "未提供基本认证信息",
		})
		return fmt.Errorf("未提供基本认证信息")
	}

	// 验证用户名和密码
	if !p.validateBasicAuth(username, password) {
		ctx.JSON(http.StatusUnauthorized, gin.H{
			"error": "认证失败",
		})
		return fmt.Errorf("认证失败")
	}

	return nil
}

// validateBasicAuth 验证基本认证
func (p *AuthPlugin) validateBasicAuth(username, password string) bool {
	// TODO: 实现实际的认证逻辑
	return true
}
