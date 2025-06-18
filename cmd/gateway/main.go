package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"gateway-go/internal/config"
	"gateway-go/internal/logger"
	"gateway-go/internal/plugin"
	"gateway-go/internal/plugin/plugins/circuitbreaker"
	"gateway-go/internal/plugin/plugins/consistency"
	"gateway-go/internal/plugin/plugins/cors"
	errorplugin "gateway-go/internal/plugin/plugins/error"
	"gateway-go/internal/plugin/plugins/interface_auth"
	"gateway-go/internal/plugin/plugins/ipwhitelist"
	"gateway-go/internal/plugin/plugins/ratelimit"
	"gateway-go/internal/router"

	"github.com/gin-gonic/gin"
)

var (
	configManager *config.ConfigManager
	pluginManager *plugin.Manager
	routerManager *router.Manager
	globalServer  *http.Server
	globalEngine  *gin.Engine
	routeHandlers map[string]gin.HandlerFunc // 存储路由处理器
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
		log.Fatalf("启动服务失败: %v", err)
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
	pid, err := readPIDFile()
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

	// 初始化日志系统
	cfg := configManager.GetConfig()
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

	// 初始化路由处理器映射
	routeHandlers = make(map[string]gin.HandlerFunc)

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
	globalServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: globalEngine,
	}

	// 写入PID文件
	if err := writePIDFile(); err != nil {
		return fmt.Errorf("写入PID文件失败: %w", err)
	}
	defer os.Remove(PIDFile)

	// 启动HTTP服务
	go func() {
		fmt.Printf("启动HTTP服务器，监听端口: %d\n", cfg.Server.Port)
		if err := globalServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP服务器启动失败: %v", err)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("正在关闭服务器...")

	// 停止配置管理器
	configManager.Stop()

	return nil
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

	// 注册错误处理插件
	if err := pluginManager.Register(errorplugin.New()); err != nil {
		log.Printf("注册错误处理插件失败: %v", err)
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

// buildEngine 构建gin引擎
func buildEngine() *gin.Engine {
	r := gin.New()

	// 使用基础的gin中间件
	r.Use(gin.Recovery())

	registerConfigRoutes(r)
	registerRoutes(r)
	return r
}

// reloadRoutes 重新加载路由
func reloadRoutes() {
	// 清除现有路由
	globalEngine = gin.New()
	globalEngine.Use(gin.Recovery())

	// 重新注册配置管理路由
	registerConfigRoutes(globalEngine)

	// 重新注册业务路由
	registerRoutes(globalEngine)

	// 更新服务器处理器
	globalServer.Handler = globalEngine

	fmt.Println("✓ 路由已重新加载")
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
	cfg := configManager.GetConfig()
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
		path := c.Request.URL.Path
		var matchedRoute *config.RouteConfig

		// 查找匹配的路由
		for _, route := range routes {
			if matchRoute(path, route.Match) {
				matchedRoute = &route
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

		// 执行插件链
		if err := pluginManager.Execute(c, matchedRoute.Name); err != nil {
			c.JSON(500, gin.H{
				"error": err.Error(),
			})
			c.Abort()
			return
		}

		// 如果请求被中止，直接返回
		if c.IsAborted() {
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
			c.JSON(500, gin.H{
				"error": fmt.Sprintf("无效的目标URL: %v", err),
			})
			c.Abort()
			return
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
			c.JSON(http.StatusBadGateway, gin.H{
				"error": fmt.Sprintf("代理请求失败: %v", err),
			})
		}

		// 执行代理请求
		proxy.ServeHTTP(c.Writer, c.Request)
		c.Abort()
	})

	// 注册一个通配符路由来捕获所有请求
	r.NoRoute(func(c *gin.Context) {
		c.JSON(404, gin.H{
			"error": "未找到匹配的路由",
		})
	})
}

// matchRoute 检查路径是否匹配路由规则
func matchRoute(path string, match config.RouteMatch) bool {
	switch match.Type {
	case "exact":
		return path == match.Path
	case "prefix":
		return strings.HasPrefix(path, match.Path)
	default:
		return path == match.Path
	}
}

// writePIDFile 写入PID文件
func writePIDFile() error {
	pid := os.Getpid()
	return os.WriteFile(PIDFile, []byte(fmt.Sprintf("%d", pid)), 0644)
}

// readPIDFile 读取PID文件
func readPIDFile() (int, error) {
	data, err := os.ReadFile(PIDFile)
	if err != nil {
		return 0, err
	}

	var pid int
	_, err = fmt.Sscanf(string(data), "%d", &pid)
	return pid, err
}
