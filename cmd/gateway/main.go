package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"hash"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"gateway-go/internal/config"
	"gateway-go/internal/logger"
	"gateway-go/internal/netutil"
	"gateway-go/internal/plugin"
	"gateway-go/internal/plugin/plugins/circuitbreaker"
	"gateway-go/internal/plugin/plugins/consistency"
	"gateway-go/internal/plugin/plugins/cors"
	"gateway-go/internal/plugin/plugins/interface_auth"
	"gateway-go/internal/plugin/plugins/ipwhitelist"
	"gateway-go/internal/plugin/plugins/ratelimit"
	"gateway-go/internal/response"
	"gateway-go/internal/router"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var (
	configManager *config.ConfigManager
	pluginManager *plugin.Manager
	routerManager *router.Manager
	globalServer  *http.Server
	globalEngine  *gin.Engine
)

// 命令行参数
var (
	configPath = flag.String("c", "./config/config.yaml", "配置文件路径")
	testConfig = flag.Bool("t", false, "测试配置文件语法")
	signalCmd  = flag.String("s", "", "发送信号 (reload|stop|quit)")
	version    = flag.Bool("v", false, "显示版本信息")
	help       = flag.Bool("h", false, "显示帮助信息")
)

const (
	Version = "1.0.0"
	PIDFile = "/tmp/gateway.pid"
)

func main() {
	flag.Parse()

	// 显示帮助信息
	if *help {
		showHelp()
		return
	}

	// 显示版本信息
	if *version {
		showVersion()
		return
	}

	// 测试配置文件
	if *testConfig {
		if err := testConfiguration(*configPath); err != nil {
			os.Exit(1)
		}
		return
	}

	// 处理信号命令
	if *signalCmd != "" {
		if err := handleSignalCommand(*signalCmd); err != nil {
			fmt.Printf("错误: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 启动服务
	if err := startServer(); err != nil {
		fmt.Fprintf(os.Stderr, "启动服务失败: %v\n", err)
		os.Exit(1)
	}
}

// showHelp 显示帮助信息
func showHelp() {
	fmt.Printf(`Gateway Go - 高性能API网关

用法: gateway [选项]

选项:
  -c <配置文件>    指定配置文件路径 (默认: ./config/config.yaml)
  -t              测试配置文件语法
  -s <信号>       发送信号到运行中的进程
                  信号类型: reload|stop|quit
  -v              显示版本信息
  -h              显示此帮助信息

示例:
  gateway -t                    # 测试配置文件
  gateway -c /etc/gateway.yaml  # 使用指定配置文件启动
  gateway -s reload             # 重新加载配置
  gateway -s stop               # 停止服务
  gateway -s quit               # 快速停止服务

信号支持:
  SIGHUP         重新加载配置
  SIGUSR1        重新加载配置
  SIGTERM        优雅停止服务
  SIGINT         快速停止服务
`)
}

// showVersion 显示版本信息
func showVersion() {
	fmt.Printf("Gateway Go version %s\n", Version)
}

// testConfiguration 测试配置文件
func testConfiguration(configPath string) error {
	cm := config.NewConfigManager(configPath)

	fmt.Printf("测试配置文件: %s\n", configPath)

	if err := cm.TestConfig(configPath); err != nil {
		fmt.Printf("✗ 配置文件测试失败: %v\n", err)
		return err
	}

	fmt.Printf("✓ 配置文件语法正确\n")
	return nil
}

// handleSignalCommand 处理信号命令
func handleSignalCommand(signalType string) error {
	pidPath := PIDFile
	if *configPath != "" {
		if cm := config.NewConfigManager(*configPath); cm != nil {
			if err := cm.LoadConfig(*configPath); err == nil {
				pidPath = getPIDFilePath(cm.GetConfig())
			}
		}
	}
	if _, err := os.Stat(pidPath); os.IsNotExist(err) && pidPath != PIDFile {
		if _, errDefault := os.Stat(PIDFile); errDefault == nil {
			pidPath = PIDFile
		}
	}

	pid, err := readPIDFile(pidPath)
	if err != nil {
		return fmt.Errorf("读取PID文件失败: %w", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("查找进程失败: %w", err)
	}

	var signal syscall.Signal
	switch signalType {
	case "reload":
		signal = syscall.SIGHUP
		fmt.Printf("发送重载信号到进程 %d\n", pid)
	case "stop":
		signal = syscall.SIGTERM
		fmt.Printf("发送停止信号到进程 %d\n", pid)
	case "quit":
		signal = syscall.SIGINT
		fmt.Printf("发送退出信号到进程 %d\n", pid)
	default:
		return fmt.Errorf("未知的信号类型: %s", signalType)
	}

	return process.Signal(signal)
}

// startServer 启动服务器
func startServer() error {
	// 创建配置管理器
	configManager = config.NewConfigManager(*configPath)

	// 加载初始配置
	fmt.Printf("正在加载配置文件: %s\n", *configPath)
	if err := configManager.LoadConfig(*configPath); err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	// 根据配置文件设置 gin 运行模式
	cfg := configManager.GetConfig()
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	} else if cfg.Server.Mode == "test" {
		gin.SetMode(gin.TestMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}
	// 初始化日志系统
	if err := logger.Init(&cfg.Log); err != nil {
		return fmt.Errorf("初始化日志失败: %w", err)
	}

	// 初始化插件管理器
	pluginManager = plugin.NewManager()

	// 注册所有插件
	registerPlugins()

	// 加载可用插件配置
	if err := loadAvailablePlugins(cfg); err != nil {
		return fmt.Errorf("加载可用插件失败: %w", err)
	}

	// 加载路由插件
	if err := loadRoutePlugins(cfg); err != nil {
		return fmt.Errorf("加载路由插件失败: %w", err)
	}

	// 初始化路由管理器
	routerManager = router.NewManagerFromConfig(configManager, pluginManager)

	// 添加配置重载钩子
	configManager.AddReloadHook(func(cfg *config.Config) error {
		fmt.Println("正在重新加载路由配置...")
		if err := routerManager.ReloadFromConfig(configManager, pluginManager); err != nil {
			return err
		}
		// 重新加载可用插件
		if err := loadAvailablePlugins(cfg); err != nil {
			return err
		}
		// 重新加载路由插件
		if err := loadRoutePlugins(cfg); err != nil {
			return err
		}
		// 重新注册路由
		reloadRoutes()
		return nil
	})

	// 启动配置监视
	if err := configManager.WatchConfig(); err != nil {
		return fmt.Errorf("启动配置监视失败: %w", err)
	}

	// 启动重载工作协程
	configManager.StartReloadWorker()

	// 处理系统信号
	configManager.HandleSignals()

	// 构建HTTP服务器
	globalEngine = buildEngine()
	globalServer = newHTTPServer(cfg, globalEngine)

	// 写入PID文件
	pidPath := getPIDFilePath(cfg)
	if err := writePIDFile(pidPath); err != nil {
		return fmt.Errorf("写入PID文件失败: %w", err)
	}
	defer os.Remove(pidPath)

	return runServer(context.Background(), globalServer, cfg, pluginManager, configManager)
}

// newHTTPServer 根据配置构建 http.Server 实例
func newHTTPServer(cfg *config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           handler,
		ReadTimeout:       cfg.Server.ReadTimeout,
		ReadHeaderTimeout: cfg.Server.GetReadHeaderTimeout(),
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		MaxHeaderBytes:    cfg.Server.MaxHeaderBytes,
	}
}

// GracefulServer 封装服务器生命周期及优雅关闭
type GracefulServer struct {
	server        *http.Server
	pluginManager *plugin.Manager
	configManager *config.ConfigManager
	timeout       time.Duration
	shutdownOnce  sync.Once
	shutdownErr   error
}

// NewGracefulServer 创建优雅退出服务器实例
func NewGracefulServer(srv *http.Server, pm *plugin.Manager, cm *config.ConfigManager, timeout time.Duration) *GracefulServer {
	return &GracefulServer{
		server:        srv,
		pluginManager: pm,
		configManager: cm,
		timeout:       timeout,
	}
}

// Shutdown 优雅关闭服务器（幂等操作，多次调用不 panic）
func (gs *GracefulServer) Shutdown(ctx context.Context) error {
	gs.shutdownOnce.Do(func() {
		gs.shutdownErr = gs.doShutdown(ctx)
	})
	return gs.shutdownErr
}

func (gs *GracefulServer) doShutdown(ctx context.Context) error {
	var shutdownErr error

	timeout := gs.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 1. 停止接受新连接并等待在途请求处理完成
	if err := gs.server.Shutdown(shutdownCtx); err != nil {
		// 超时或发生错误时强制关闭所有活跃连接
		_ = gs.server.Close()
		shutdownErr = fmt.Errorf("server shutdown error: %w", err)
	}

	// 2. 停止所有插件
	if gs.pluginManager != nil {
		if err := gs.pluginManager.StopAll(); err != nil {
			if shutdownErr == nil {
				shutdownErr = fmt.Errorf("plugin stop error: %w", err)
			}
		}
	}

	// 3. 停止配置管理器
	if gs.configManager != nil {
		gs.configManager.Stop()
	}

	// 4. 同步日志
	_ = logger.Sync()

	return shutdownErr
}

// runServer 运行 HTTP 服务器并监听中断信号与运行错误
func runServer(ctx context.Context, srv *http.Server, cfg *config.Config, pm *plugin.Manager, cm *config.ConfigManager) error {
	gs := NewGracefulServer(srv, pm, cm, cfg.Server.GracefulShutdownTimeout)
	errCh := make(chan error, 1)

	// 启动 HTTP 服务，启动/运行错误通过 errCh 传递到主流程
	go func() {
		fmt.Printf("启动HTTP服务器，监听端口: %d\n", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// 监听中断信号 (SIGINT, SIGTERM)
	quit := make(chan os.Signal, 2)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	done := make(chan struct{})
	defer close(done)

	select {
	case err := <-errCh:
		fmt.Printf("HTTP服务器启动或运行失败: %v\n", err)
		return fmt.Errorf("http server failed: %w", err)
	case sig := <-quit:
		fmt.Printf("收到信号 %v，正在优雅关闭服务器...\n", sig)
		// 监听后续重复信号，使用 sync.Once 保护，不 panic
		go func() {
			for {
				select {
				case s, ok := <-quit:
					if !ok {
						return
					}
					fmt.Printf("再次收到信号 %v，已在关闭中\n", s)
					_ = gs.Shutdown(context.Background())
				case <-done:
					return
				}
			}
		}()
		if err := gs.Shutdown(context.Background()); err != nil {
			fmt.Printf("优雅关闭失败: %v\n", err)
			return err
		}
		fmt.Println("服务器已优雅退出")
		return nil
	case <-ctx.Done():
		fmt.Println("上下文取消，正在优雅关闭服务器...")
		if err := gs.Shutdown(context.Background()); err != nil {
			fmt.Printf("优雅关闭失败: %v\n", err)
			return err
		}
		fmt.Println("服务器已优雅退出")
		return nil
	}
}

// registerPlugins 注册所有插件
func registerPlugins() {
	// 注册限流插件
	if err := pluginManager.Register(ratelimit.New()); err != nil {
		log.Printf("注册限流插件失败: %v", err)
	}

	// 注册熔断器插件
	if err := pluginManager.Register(circuitbreaker.New()); err != nil {
		log.Printf("注册熔断器插件失败: %v", err)
	}

	// 注册跨域插件
	if err := pluginManager.Register(cors.New()); err != nil {
		log.Printf("注册跨域插件失败: %v", err)
	}

	// 注册IP白名单插件
	if err := pluginManager.Register(ipwhitelist.New()); err != nil {
		log.Printf("注册IP白名单插件失败: %v", err)
	}

	// 注册一致性校验插件
	if err := pluginManager.Register(consistency.New()); err != nil {
		log.Printf("注册一致性校验插件失败: %v", err)
	}

	// 注册外部接口认证插件
	if err := pluginManager.Register(interface_auth.New()); err != nil {
		log.Printf("注册外部接口认证插件失败: %v", err)
	}

	fmt.Println("✓ 所有插件已注册")
}

// loadAvailablePlugins 加载可用插件配置
func loadAvailablePlugins(cfg *config.Config) error {
	if cfg.Plugins.Available == nil {
		return nil
	}

	// 转换配置格式
	var pluginConfigs []plugin.PluginConfig
	for _, p := range cfg.Plugins.Available {
		pluginConfigs = append(pluginConfigs, plugin.PluginConfig{
			Name:    p.Name,
			Enabled: p.Enabled,
			Order:   p.Order,
			Config:  p.Config,
		})
	}

	// 加载可用插件
	if err := pluginManager.LoadAvailablePlugins(pluginConfigs); err != nil {
		return fmt.Errorf("加载可用插件失败: %v", err)
	}

	fmt.Printf("✓ 已加载 %d 个可用插件\n", len(pluginConfigs))
	return nil
}

// loadRoutePlugins 加载路由插件
func loadRoutePlugins(cfg *config.Config) error {
	for _, route := range cfg.Routes {
		if len(route.Plugins) > 0 {
			if err := pluginManager.LoadRoutePlugins(route.Name, route.Plugins); err != nil {
				return fmt.Errorf("加载路由 %s 的插件失败: %v", route.Name, err)
			}
			fmt.Printf("✓ 路由 %s 已加载插件: %v\n", route.Name, route.Plugins)
		}
	}
	return nil
}

// customRecovery 自定义恢复中间件，确保未捕获 panic 统一经由 respondError 输出标准结构
func customRecovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		reqID := response.GetRequestID(c)
		if logger.Log != nil && logger.Log.Core().Enabled(zap.ErrorLevel) {
			logger.Log.Error("服务器内部发生 panic",
				zap.Any("error", recovered),
				zap.String("request_id", reqID),
			)
		}
		respondError(c, http.StatusInternalServerError, "服务器内部错误", reqID)
	})
}

// buildEngine 构建gin引擎
func buildEngine() *gin.Engine {
	var cfg *config.Config
	if configManager != nil {
		cfg = configManager.GetConfig()
	}
	return buildEngineWithConfig(cfg)
}

// buildEngineWithConfig 使用给定配置构建gin引擎
func buildEngineWithConfig(cfg *config.Config) *gin.Engine {
	r := gin.New()

	// 注册链路最外层中间件：Access Log 与统一错误输出的 customRecovery
	r.Use(accessLogMiddleware())
	r.Use(customRecovery())

	var trustedProxies []string
	if cfg != nil {
		trustedProxies = cfg.Server.TrustedProxies
	}

	if len(trustedProxies) > 0 {
		_ = r.SetTrustedProxies(trustedProxies)
	} else {
		_ = r.SetTrustedProxies(nil)
		r.ForwardedByClientIP = false
	}
	netutil.SetDefaultTrustedProxies(trustedProxies)

	registerConfigRoutes(r)
	registerRoutesWithConfig(r, cfg)
	return r
}

// reloadRoutes 重新加载路由
func reloadRoutes() {
	// 清除现有路由并重新构建
	globalEngine = buildEngine()

	// 更新服务器处理器
	globalServer.Handler = globalEngine

	fmt.Println("✓ 路由已重新加载")
}

// accessLogMiddleware 构建链路最外层的独立访问日志中间件 (REQ-P0-05)
func accessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()

		defer func() {
			if logger.Log == nil {
				return
			}

			var logCfg *config.LogConfig
			if configManager != nil {
				if cfg := configManager.GetConfig(); cfg != nil {
					logCfg = &cfg.Log
				}
			}

			if logCfg != nil && !logCfg.IsAccessLogEnabled() {
				return
			}

			fields := buildAccessFields(c, logCfg, startTime)
			logger.Log.Info("access", fields...)
		}()

		c.Next()
	}
}

// getErrorKind 根据响应状态码推断错误类型
func getErrorKind(c *gin.Context, status int) string {
	if ek := c.GetString("error_kind"); ek != "" {
		return ek
	}
	switch {
	case status == http.StatusBadGateway:
		return "bad_gateway"
	case status == http.StatusGatewayTimeout:
		return "timeout"
	case status == http.StatusNotFound:
		return "not_found"
	case status >= 500:
		return "upstream_error"
	default:
		return "-"
	}
}

// buildAccessFields 构造 access log 字段列表并支持 access_fields 白名单过滤
func buildAccessFields(c *gin.Context, cfg *config.LogConfig, startTime time.Time) []zap.Field {
	cost := time.Since(startTime)
	rtMs := cost.Milliseconds()

	statusCode := c.Writer.Status()
	if statusCode <= 0 {
		statusCode = http.StatusOK
	}

	bytesWritten := int64(c.Writer.Size())
	if bytesWritten < 0 {
		bytesWritten = 0
	}

	upstream := c.GetString("target")
	if upstream == "" {
		upstream = "-"
	}

	reqID := c.GetHeader("X-Request-ID")
	if reqID == "" {
		reqID = c.GetHeader("x-request-id")
	}
	if reqID == "" {
		reqID = c.GetString("request_id")
	}
	if reqID == "" {
		reqID = "-"
	}

	clientIP := c.ClientIP()
	if clientIP == "" {
		clientIP = "-"
	}

	errorKind := getErrorKind(c, statusCode)
	method := c.Request.Method
	path := c.Request.URL.Path

	var accessFieldsConfig []string
	if cfg != nil {
		accessFieldsConfig = cfg.AccessFields
	}

	// 构造字段映射表供白名单过滤使用
	fieldMap := map[string]zap.Field{
		"status":     zap.Int("status", statusCode),
		"bytes":      zap.Int64("bytes", bytesWritten),
		"upstream":   zap.String("upstream", upstream),
		"target":     zap.String("target", upstream),
		"rt_ms":      zap.Int64("rt_ms", rtMs),
		"request_id": zap.String("request_id", reqID),
		"client_ip":  zap.String("client_ip", clientIP),
		"error_kind": zap.String("error_kind", errorKind),
		"method":     zap.String("method", method),
		"path":       zap.String("path", path),
		"cost":       zap.Duration("cost", cost),
	}

	if v, ok := c.Get("body_bytes"); ok {
		fieldMap["body_bytes"] = zap.Int64("body_bytes", v.(int64))
	}
	if v, ok := c.Get("body_sha256"); ok && v.(string) != "" {
		fieldMap["body_sha256"] = zap.String("body_sha256", v.(string))
	}
	if v, ok := c.Get("req_body_bytes"); ok {
		fieldMap["req_body_bytes"] = zap.Int64("req_body_bytes", v.(int64))
	}
	if v, ok := c.Get("req_body_sha256"); ok && v.(string) != "" {
		fieldMap["req_body_sha256"] = zap.String("req_body_sha256", v.(string))
	}
	if v, ok := c.Get("resp_body_bytes"); ok {
		fieldMap["resp_body_bytes"] = zap.Int64("resp_body_bytes", v.(int64))
	}
	if v, ok := c.Get("resp_body_sha256"); ok && v.(string) != "" {
		fieldMap["resp_body_sha256"] = zap.String("resp_body_sha256", v.(string))
	}
	if v, ok := c.Get("sample_req_body"); ok {
		fieldMap["body"] = zap.String("body", v.(string))
	}
	if v, ok := c.Get("sample_resp_body"); ok {
		fieldMap["resp_body"] = zap.String("resp_body", v.(string))
	}

	// 如果配置了 AccessFields 白名单，则仅输出指定的字段
	if len(accessFieldsConfig) > 0 {
		var fields []zap.Field
		for _, name := range accessFieldsConfig {
			if f, exists := fieldMap[name]; exists {
				fields = append(fields, f)
			}
		}
		return fields
	}

	// 默认输出标准字段 (status, bytes, upstream, rt_ms, request_id, client_ip, error_kind, method, path)
	fields := []zap.Field{
		zap.Int("status", statusCode),
		zap.Int64("bytes", bytesWritten),
		zap.String("upstream", upstream),
		zap.Int64("rt_ms", rtMs),
		zap.String("request_id", reqID),
		zap.String("client_ip", clientIP),
		zap.String("error_kind", errorKind),
		zap.String("method", method),
		zap.String("path", path),
	}

	// 报文流式观测扩展字段（若存在则附加）
	if f, exists := fieldMap["body_bytes"]; exists {
		fields = append(fields, f)
	}
	if f, exists := fieldMap["body_sha256"]; exists {
		fields = append(fields, f)
	}
	if f, exists := fieldMap["req_body_bytes"]; exists {
		fields = append(fields, f)
	}
	if f, exists := fieldMap["req_body_sha256"]; exists {
		fields = append(fields, f)
	}
	if f, exists := fieldMap["resp_body_bytes"]; exists {
		fields = append(fields, f)
	}
	if f, exists := fieldMap["resp_body_sha256"]; exists {
		fields = append(fields, f)
	}
	if f, exists := fieldMap["body"]; exists {
		fields = append(fields, f)
	}
	if f, exists := fieldMap["resp_body"]; exists {
		fields = append(fields, f)
	}

	return fields
}

// registerConfigRoutes 注册配置管理路由
func registerConfigRoutes(r *gin.Engine) {
	// 健康检查路由
	r.GET("/gatewaygo/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
}

// registerRoutes 注册业务路由
func registerRoutes(r *gin.Engine) {
	if configManager == nil {
		return
	}
	cfg := configManager.GetConfig()
	registerRoutesWithConfig(r, cfg)
}

// registerRoutesWithConfig 使用指定配置注册业务路由
func registerRoutesWithConfig(r *gin.Engine, cfg *config.Config) {
	if cfg == nil {
		return
	}

	// 按优先级排序路由
	routes := make([]config.RouteConfig, len(cfg.Routes))
	copy(routes, cfg.Routes)
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Match.Priority > routes[j].Match.Priority
	})

	// 创建路由处理中间件
	r.Use(func(c *gin.Context) {
		startTime := time.Now()

		bodyLimit := 0
		enableSHA := true
		if configManager != nil {
			if cfg := configManager.GetConfig(); cfg != nil {
				bodyLimit = cfg.Log.BodyLimit
				enableSHA = cfg.Log.IsBodySHA256Enabled()
			}
		}

		var reqObserver *bodyObserver
		if c.Request.Body != nil && c.Request.Body != http.NoBody {
			reqObserver = newBodyObserver(c.Request.Body, bodyLimit, enableSHA)
			c.Request.Body = reqObserver
		}

		if logger.Log != nil && logger.Log.Core().Enabled(zap.DebugLevel) {
			req := c.Request
			headers := make(map[string][]string)
			for k, v := range req.Header {
				headers[k] = v
			}
			// 1. 收到请求 (注意: 不再读取或回写 req.Body，避免 4KB 截断)
			logger.Log.Debug("收到请求",
				zap.String("method", req.Method),
				zap.String("path", req.URL.Path),
				zap.String("raw_query", req.URL.RawQuery),
				zap.Any("standard_headers", headers),
				zap.String("client_ip", netutil.ClientIP(c, cfg.Server.TrustedProxies)),
				zap.Time("start_time", startTime),
			)
			c.Set("_debug_start_time", startTime)
			c.Set("_debug_headers", headers)
		}

		path := c.Request.URL.Path
		var matchedRoute *config.RouteConfig

		// 查找匹配的路由
		for _, route := range routes {
			if matchRoute(path, route.Match, c) {
				matchedRoute = &route
				if logger.Log != nil && logger.Log.Core().Enabled(zap.DebugLevel) {
					headers, _ := c.Get("_debug_headers")
					logger.Log.Debug("匹配到路由",
						zap.String("route_name", route.Name),
						zap.String("method", c.Request.Method),
						zap.String("path", c.Request.URL.Path),
						zap.String("match_type", route.Match.Type),
						zap.String("match_path", route.Match.Path),
						zap.Any("standard_headers", headers),
					)
				}
				break
			}
		}

		// 如果没有匹配的路由，继续下一个处理器
		if matchedRoute == nil {
			c.Next()
			return
		}

		// 设置目标信息到上下文
		c.Set("target", matchedRoute.Target.URL)

		reqID := response.GetRequestID(c)

		// 执行插件链
		if err := pluginManager.Execute(c, matchedRoute.Name); err != nil {
			failedPlugin, _ := c.Get("failed_plugin")
			pluginName, _ := failedPlugin.(string)
			if c.Writer.Written() {
				if logger.Log != nil && logger.Log.Core().Enabled(zap.WarnLevel) {
					logger.Log.Warn("插件已写响应但返回错误(违反契约，忽略该错误)",
						zap.String("route_name", matchedRoute.Name),
						zap.String("plugin", pluginName),
						zap.String("request_id", reqID),
						zap.Error(err),
					)
				}
				c.Abort()
				return
			}

			if logger.Log != nil && logger.Log.Core().Enabled(zap.WarnLevel) {
				logger.Log.Warn("插件执行失败",
					zap.String("route_name", matchedRoute.Name),
					zap.String("plugin", pluginName),
					zap.String("request_id", reqID),
					zap.Error(err),
				)
			}
			respondError(c, 500, err.Error(), reqID)
			return
		}

		// 如果请求被中止或已写响应，直接返回
		if c.IsAborted() || c.Writer.Written() {
			return
		}

		// 检查是否为内部响应配置
		if strings.HasPrefix(matchedRoute.Target.URL, "internal://") {
			// 处理内部响应
			if matchedRoute.Response != nil {
				// 设置内容类型
				if matchedRoute.Response.ContentType != "" {
					c.Header("Content-Type", matchedRoute.Response.ContentType)
				} else {
					c.Header("Content-Type", "text/plain")
				}

				// 返回自定义响应
				c.String(matchedRoute.Response.Status, matchedRoute.Response.Content)
			} else {
				// 默认响应
				c.String(200, "gateway-go running")
			}
			c.Abort()
			return
		}

		// 创建反向代理
		target, err := url.Parse(matchedRoute.Target.URL)
		if err != nil {
			if logger.Log != nil && logger.Log.Core().Enabled(zap.WarnLevel) {
				logger.Log.Warn("目标URL无效",
					zap.String("route_name", matchedRoute.Name),
					zap.String("target_url", matchedRoute.Target.URL),
					zap.String("request_id", reqID),
					zap.String("error", err.Error()),
				)
			}
			respondError(c, 500, fmt.Sprintf("无效的目标URL: %v", err), reqID)
			return
		}
		if logger.Log != nil && logger.Log.Core().Enabled(zap.DebugLevel) {
			logger.Log.Debug("开始转发请求",
				zap.String("target_url", matchedRoute.Target.URL),
				zap.String("method", c.Request.Method),
				zap.String("path", c.Request.URL.Path),
			)
		}

		// 处理路径前缀
		proxyPath := path
		if matchedRoute.Match.Type == "prefix" && matchedRoute.Match.Path != "/" {
			proxyPath = strings.TrimPrefix(path, matchedRoute.Match.Path)
			if !strings.HasPrefix(proxyPath, "/") {
				proxyPath = "/" + proxyPath
			}
		}

		// 创建反向代理
		proxy := httputil.NewSingleHostReverseProxy(target)
		// 设置自定义的 Director
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.URL.Path = proxyPath
			req.Header = c.Request.Header
			req.Header.Set("X-Forwarded-Host", c.Request.Host)
			req.Header.Set("X-Origin-Host", target.Host)
		}
		// 设置错误处理
		proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
			if logger.Log != nil && logger.Log.Core().Enabled(zap.WarnLevel) {
				logger.Log.Warn("反向代理失败",
					zap.String("route_name", matchedRoute.Name),
					zap.String("target_url", matchedRoute.Target.URL),
					zap.String("request_id", reqID),
					zap.String("error", err.Error()),
				)
			}
			respondError(c, http.StatusBadGateway, fmt.Sprintf("代理请求失败: %v", err), reqID)
		}
		// 观察后端响应体 (流式旁路观察，禁止截断或回写)
		var respObserver *bodyObserver
		var respStatusCode int
		proxy.ModifyResponse = func(resp *http.Response) error {
			respStatusCode = resp.StatusCode
			if resp.Body != nil {
				respObserver = newBodyObserver(resp.Body, bodyLimit, enableSHA)
				resp.Body = respObserver
			}
			return nil
		}
		// 执行代理请求
		proxy.ServeHTTP(c.Writer, c.Request)
		c.Abort()

		cost := time.Since(startTime)

		var reqBytes int64
		var reqSHA string
		var reqSample []byte
		if reqObserver != nil {
			reqBytes = reqObserver.BytesCount()
			reqSHA = reqObserver.SHA256Hex()
			reqSample = reqObserver.Sample()
		}

		var respBytes int64
		var respSHA string
		var respSample []byte
		if respObserver != nil {
			respBytes = respObserver.BytesCount()
			respSHA = respObserver.SHA256Hex()
			respSample = respObserver.Sample()
		}

		// 主报文统计信息
		payloadBytes := reqBytes
		payloadSHA := reqSHA
		if payloadBytes == 0 && respBytes > 0 {
			payloadBytes = respBytes
			payloadSHA = respSHA
		}

		if logger.Log != nil && logger.Log.Core().Enabled(zap.DebugLevel) {
			// 收到后端响应日志
			respFields := []zap.Field{
				zap.String("target_url", matchedRoute.Target.URL),
				zap.Int("status", respStatusCode),
				zap.Int64("body_bytes", respBytes),
				zap.String("body_sha256", respSHA),
				zap.Int64("resp_body_bytes", respBytes),
				zap.String("resp_body_sha256", respSHA),
			}
			if bodyLimit > 0 && len(respSample) > 0 {
				respFields = append(respFields, zap.String("resp_body", string(respSample)))
			}
			logger.Log.Debug("收到后端响应", respFields...)

			// 请求体转发完成日志 (如果有请求体)
			if reqBytes > 0 {
				reqFields := []zap.Field{
					zap.String("target_url", matchedRoute.Target.URL),
					zap.Int64("body_bytes", reqBytes),
					zap.String("body_sha256", reqSHA),
					zap.Int64("req_body_bytes", reqBytes),
					zap.String("req_body_sha256", reqSHA),
				}
				if bodyLimit > 0 && len(reqSample) > 0 {
					reqFields = append(reqFields, zap.String("body", string(reqSample)))
				}
				logger.Log.Debug("请求转发完成", reqFields...)
			}

			// 返回给用户
			userFields := []zap.Field{
				zap.String("method", c.Request.Method),
				zap.String("path", c.Request.URL.Path),
				zap.Duration("cost", cost),
				zap.String("client_ip", netutil.ClientIP(c, cfg.Server.TrustedProxies)),
				zap.String("target", target),
				zap.Int64("body_bytes", payloadBytes),
				zap.String("body_sha256", payloadSHA),
			}
			logger.Log.Debug("返回给用户", userFields...)
		}

		// 存储报文观测统计值至上下文供外层访问日志读取
		c.Set("body_bytes", payloadBytes)
		c.Set("body_sha256", payloadSHA)
		c.Set("req_body_bytes", reqBytes)
		c.Set("req_body_sha256", reqSHA)
		c.Set("resp_body_bytes", respBytes)
		c.Set("resp_body_sha256", respSHA)
		if bodyLimit > 0 {
			if len(reqSample) > 0 {
				c.Set("sample_req_body", string(reqSample))
			}
			if len(respSample) > 0 {
				c.Set("sample_resp_body", string(respSample))
			}
		}
	})

	// 注册一个通配符路由来捕获所有请求
	r.NoRoute(func(c *gin.Context) {
		reqID := response.GetRequestID(c)
		if logger.Log != nil && logger.Log.Core().Enabled(zap.WarnLevel) {
			logger.Log.Warn("未匹配到路由",
				zap.String("method", c.Request.Method),
				zap.String("path", c.Request.URL.Path),
				zap.String("client_ip", netutil.ClientIP(c, cfg.Server.TrustedProxies)),
				zap.String("request_id", reqID),
			)
		}
		respondError(c, 404, "未找到匹配的路由", reqID)
	})
}

// respondError 统一写出错误响应
func respondError(c *gin.Context, code int, message string, requestID string) {
	response.RespondError(c, code, message, requestID)
}

// matchRoute 检查路径是否匹配路由规则
func matchRoute(path string, match config.RouteMatch, c *gin.Context) bool {
	// 路径匹配
	matched := false
	switch match.Type {
	case "exact":
		matched = path == match.Path
	case "prefix":
		matched = strings.HasPrefix(path, match.Path)
	default:
		matched = path == match.Path
	}
	if !matched {
		return false
	}
	// Host 匹配
	if match.Host != "" && c.Request.Host != match.Host {
		return false
	}
	// Method 匹配
	if match.Method != "" && c.Request.Method != match.Method {
		return false
	}
	// Headers 匹配
	for k, v := range match.Headers {
		reqVal := c.GetHeader(k)
		if reqVal != v {
			return false
		}
	}
	// QueryParams 匹配
	for k, v := range match.QueryParams {
		if c.Query(k) != v {
			return false
		}
	}
	return true
}

// getPIDFilePath 获取 PID 文件路径
func getPIDFilePath(cfg *config.Config) string {
	if cfg != nil {
		return cfg.Server.GetPIDFile()
	}
	return PIDFile
}

// writePIDFile 写入PID文件
func writePIDFile(path string) error {
	pid := os.Getpid()
	return os.WriteFile(path, []byte(fmt.Sprintf("%d", pid)), 0644)
}

// readPIDFile 读取PID文件
func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	var pid int
	_, err = fmt.Sscanf(string(data), "%d", &pid)
	return pid, err
}

// bodyObserver 是一个非侵入的流式 ReadCloser 包装器，
// 用于在不截断、不整体缓冲进内存的前提下计算传输字节数、SHA-256 及采样前 body_limit 字节。
type bodyObserver struct {
	rc         io.ReadCloser
	hasher     hash.Hash
	limit      int
	sampleBuf  bytes.Buffer
	bytesCount int64
	closed     bool
	mu         sync.Mutex
}

func newBodyObserver(rc io.ReadCloser, limit int, enableSHA bool) *bodyObserver {
	var h hash.Hash
	if enableSHA {
		h = sha256.New()
	}
	return &bodyObserver{
		rc:     rc,
		hasher: h,
		limit:  limit,
	}
}

func (b *bodyObserver) Read(p []byte) (int, error) {
	if b.rc == nil {
		return 0, io.EOF
	}
	n, err := b.rc.Read(p)
	if n > 0 {
		b.mu.Lock()
		b.bytesCount += int64(n)
		if b.hasher != nil {
			b.hasher.Write(p[:n])
		}
		if b.limit > 0 && b.sampleBuf.Len() < b.limit {
			toWrite := n
			remaining := b.limit - b.sampleBuf.Len()
			if toWrite > remaining {
				toWrite = remaining
			}
			b.sampleBuf.Write(p[:toWrite])
		}
		b.mu.Unlock()
	}
	return n, err
}

func (b *bodyObserver) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	if b.rc != nil {
		return b.rc.Close()
	}
	return nil
}

func (b *bodyObserver) BytesCount() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bytesCount
}

func (b *bodyObserver) SHA256Hex() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hasher == nil || b.bytesCount == 0 {
		return ""
	}
	sum := b.hasher.Sum(nil)
	full := hex.EncodeToString(sum)
	if len(full) >= 8 {
		return full[:8]
	}
	return full
}

func (b *bodyObserver) Sample() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sampleBuf.Bytes()
}
