package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册所有路由
func RegisterRoutes(r *gin.Engine, manager *Manager) {
	// 使用路由管理器处理所有请求
	r.NoRoute(func(c *gin.Context) {
		target, err := manager.MatchRoute(c)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{
				"error": err.Error(),
			})
			return
		}

		// TODO: 实现请求转发逻辑
		// 这里可以添加代理转发、负载均衡等功能
		c.JSON(http.StatusOK, gin.H{
			"message": "路由匹配成功",
			"target":  target,
		})
	})

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status": "ok",
		})
	})
}
