package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 使用路由管理器注册兜底路由处理（清理原TODO及重复健康检查）
func RegisterRoutes(r *gin.Engine, manager *Manager, handlers ...gin.HandlerFunc) {
	if r == nil || manager == nil {
		return
	}

	allHandlers := append([]gin.HandlerFunc{
		func(c *gin.Context) {
			route, err := manager.MatchRoute(c)
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{
					"error": err.Error(),
				})
				return
			}

			c.Set("matched_route", route)
			c.Next()
			if !c.Writer.Written() {
				c.JSON(http.StatusOK, gin.H{
					"message": "路由匹配成功",
					"route":   route.Name,
				})
			}
		},
	}, handlers...)

	r.NoRoute(allHandlers...)
}
