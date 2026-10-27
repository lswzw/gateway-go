package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 使用路由管理器注册兜底路由处理（清理原TODO及重复健康检查）
func RegisterRoutes(r *gin.Engine, manager *Manager) {
	if r == nil || manager == nil {
		return
	}

	r.NoRoute(func(c *gin.Context) {
		route, err := manager.MatchRoute(c)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{
				"error": err.Error(),
			})
			return
		}

		c.Set("matched_route", route)
		c.Next()
	})
}
