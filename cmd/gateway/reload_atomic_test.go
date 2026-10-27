package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gateway-go/internal/config"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// writeTestConfigFile 写入测试配置文件
func writeTestConfigFile(t *testing.T, dir string, targetURL string, routePath string, priority int) string {
	t.Helper()
	cfgContent := fmt.Sprintf(`server:
  port: 8080
  mode: "test"
  read_timeout: 10s
  write_timeout: 10s
  idle_timeout: 30s
  max_header_bytes: 1048576
  graceful_shutdown_timeout: 5s
log:
  level: "info"
  format: "json"
  output: "stdout"
  max_size: 100
  max_age: 30
  max_backups: 10
routes:
  - name: "test-route"
    match:
      type: "prefix"
      path: "%s"
      priority: %d
    target:
      url: "%s"
`, routePath, priority, targetURL)

	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("写入测试配置文件失败: %v", err)
	}
	return cfgPath
}

// TestReloadAtomic 高并发无锁热重载测试 (REQ-P1-02 前半)
// 验收标准：50 并发工作者连续发送 1000+ 请求，同时触发 15-20 次配置重载；
// 在 -race 模式下运行，零 5xx 错误、零 panic、零数据竞争。
func TestReloadAtomic(t *testing.T) {
	SetSnapshotGracePeriod(20 * time.Millisecond)
	defer SetSnapshotGracePeriod(2 * time.Second)

	// 1. 启动模拟上游服务
	var upstreamReqCount atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamReqCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	// 2. 初始化配置文件与配置管理器
	tempDir := t.TempDir()
	cfgPath := writeTestConfigFile(t, tempDir, upstream.URL, "/api", 10)

	cm := config.NewConfigManager(cfgPath)
	if err := cm.LoadConfig(cfgPath); err != nil {
		t.Fatalf("加载初始配置失败: %v", err)
	}

	// 3. 构建初始快照并绑定固定分发器
	initialEngine := buildEngineWithConfig(cm.GetConfig())
	initialSnap := &Snapshot{
		Version:   cm.GetVersion(),
		Config:    cm.GetConfig(),
		Engine:    initialEngine,
		CreatedAt: time.Now(),
	}
	dispatcher := NewDispatcher(initialSnap)
	setupReloadLifecycle(cm, dispatcher)

	// 4. 启动网关服务 (Handler 为固定的 dispatcher)
	gwServer := httptest.NewServer(dispatcher)
	defer gwServer.Close()

	// 5. 准备高并发 HTTP 客户端
	tr := &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     30 * time.Second,
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   5 * time.Second,
	}

	// 统计指标
	var (
		totalRequests   atomic.Int64
		successRequests atomic.Int64
		err5xxCount     atomic.Int64
		otherErrCount   atomic.Int64
		panicCount      atomic.Int64
	)

	const (
		concurrencyWorkers = 50
		requestsPerWorker  = 25 // 50 * 25 = 1250 requests total (> 1000)
		reloadCycles       = 20
	)

	stopTraffic := make(chan struct{})
	var workersWg sync.WaitGroup

	// 启动 50 个并发请求协程
	for w := 0; w < concurrencyWorkers; w++ {
		workersWg.Add(1)
		go func(workerID int) {
			defer func() {
				if r := recover(); r != nil {
					panicCount.Add(1)
				}
				workersWg.Done()
			}()

			reqURL := fmt.Sprintf("%s/api/test?worker=%d", gwServer.URL, workerID)
			for i := 0; i < requestsPerWorker; i++ {
				select {
				case <-stopTraffic:
					return
				default:
				}

				resp, err := client.Get(reqURL)
				totalRequests.Add(1)
				if err != nil {
					otherErrCount.Add(1)
					continue
				}

				if resp.StatusCode >= 500 {
					err5xxCount.Add(1)
				} else if resp.StatusCode == http.StatusOK {
					successRequests.Add(1)
				} else {
					otherErrCount.Add(1)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}(w)
	}

	// 6. 在流量并发期间，同时触发 20 次快照热重载
	var reloadWg sync.WaitGroup
	reloadWg.Add(1)
	go func() {
		defer reloadWg.Done()
		for cycle := 1; cycle <= reloadCycles; cycle++ {
			// 修改配置文件触发新版本
			writeTestConfigFile(t, tempDir, upstream.URL, "/api", 10+cycle)
			if err := cm.ReloadConfig(); err != nil {
				t.Errorf("第 %d 次热重载失败: %v", cycle, err)
			}
			time.Sleep(3 * time.Millisecond)
		}
	}()

	// 等待重载与流量处理完成
	reloadWg.Wait()
	workersWg.Wait()
	close(stopTraffic)

	// 7. 断言指标：零 5xx、零 panic、零数据竞争
	t.Logf("压测结果: 总请求数=%d, 成功数=%d, 5xx错误数=%d, 其他错误数=%d, Panic数=%d, 上游处理数=%d",
		totalRequests.Load(), successRequests.Load(), err5xxCount.Load(),
		otherErrCount.Load(), panicCount.Load(), upstreamReqCount.Load())

	if panicCount.Load() > 0 {
		t.Fatalf("热重载并发压测发生 panic，panic 次数: %d", panicCount.Load())
	}
	if err5xxCount.Load() > 0 {
		t.Fatalf("热重载并发压测产生 5xx 错误，5xx 次数: %d", err5xxCount.Load())
	}
	if otherErrCount.Load() > 0 {
		t.Fatalf("热重载并发压测产生非 200 错误，次数: %d", otherErrCount.Load())
	}
	if totalRequests.Load() < 1000 {
		t.Fatalf("发送的请求数未达到 1000，当前总数: %d", totalRequests.Load())
	}
	if successRequests.Load() != totalRequests.Load() {
		t.Fatalf("成功请求数 (%d) 不等于总请求数 (%d)", successRequests.Load(), totalRequests.Load())
	}

	// 最终快照版本应递增为 21 (初始 1 + 20 次成功重载)
	currentSnap := dispatcher.Snapshot()
	if currentSnap == nil {
		t.Fatal("当前分发器快照为空")
	}
	if currentSnap.Version != "21" {
		t.Errorf("期望最终版本为 21，实际为: %s", currentSnap.Version)
	}
}

// TestReloadHookFailureKeepsOld 验证重载钩子失败时原子回滚 (REQ-P1-02 前半)
// 验收标准：钩子失败时，快照不替换、配置版本号不变、旧路由继续完全正常生效。
func TestReloadHookFailureKeepsOld(t *testing.T) {
	SetSnapshotGracePeriod(20 * time.Millisecond)
	defer SetSnapshotGracePeriod(2 * time.Second)

	// 1. 启动模拟上游
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`upstream v1`))
	}))
	defer upstream.Close()

	// 2. 初始化初始配置与快照
	tempDir := t.TempDir()
	cfgPath := writeTestConfigFile(t, tempDir, upstream.URL, "/v1/api", 10)

	cm := config.NewConfigManager(cfgPath)
	if err := cm.LoadConfig(cfgPath); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	initialEngine := buildEngineWithConfig(cm.GetConfig())
	initialSnap := &Snapshot{
		Version:   cm.GetVersion(),
		Config:    cm.GetConfig(),
		Engine:    initialEngine,
		CreatedAt: time.Now(),
	}
	dispatcher := NewDispatcher(initialSnap)
	setupReloadLifecycle(cm, dispatcher)

	gwServer := httptest.NewServer(dispatcher)
	defer gwServer.Close()

	// 3. 验证旧配置路由工作正常
	respV1, err := http.Get(gwServer.URL + "/v1/api")
	if err != nil || respV1.StatusCode != http.StatusOK {
		t.Fatalf("初次请求 /v1/api 失败: err=%v, code=%d", err, respV1.StatusCode)
	}
	_ = respV1.Body.Close()

	initialVer := cm.GetVersion()
	if initialVer != "1" {
		t.Fatalf("期望初始版本号为 1，实际为: %s", initialVer)
	}

	// 4. 注册一个必失败的重载钩子
	expectedErr := errors.New("simulated hook failure: external system unreachable")
	var hookCalled atomic.Bool
	cm.AddReloadHook(func(cfg *config.Config) error {
		hookCalled.Store(true)
		return expectedErr
	})

	// 5. 更新磁盘文件为新路由 /v2/api 并触发重载
	writeTestConfigFile(t, tempDir, upstream.URL, "/v2/api", 20)
	reloadErr := cm.ReloadConfig()

	// 6. 断言重载返回错误
	if reloadErr == nil {
		t.Fatal("期望重载返回错误，但实际返回 nil")
	}
	if !hookCalled.Load() {
		t.Fatal("期望重载钩子被执行")
	}

	// 7. 断言版本号完全未变
	afterVer := cm.GetVersion()
	if afterVer != initialVer {
		t.Fatalf("重载失败后版本号被篡改，期望保持 %s，实际为: %s", initialVer, afterVer)
	}

	// 8. 断言快照未被替换
	activeSnap := dispatcher.Snapshot()
	if activeSnap.Version != initialVer {
		t.Fatalf("分发器快照被错误替换，期望版本 %s，实际为: %s", initialVer, activeSnap.Version)
	}

	// 9. 断言旧路由仍然正常响应 (200 OK)
	respOld, err := http.Get(gwServer.URL + "/v1/api")
	if err != nil {
		t.Fatalf("回滚后旧路由请求失败: %v", err)
	}
	if respOld.StatusCode != http.StatusOK {
		t.Fatalf("回滚后旧路由响应码期望 200，实际为: %d", respOld.StatusCode)
	}
	_ = respOld.Body.Close()

	// 10. 断言新路由未生效 (404 Not Found)
	respNew, err := http.Get(gwServer.URL + "/v2/api")
	if err != nil {
		t.Fatalf("请求未生效的新路由失败: %v", err)
	}
	if respNew.StatusCode != http.StatusNotFound {
		t.Fatalf("新路由不应生效，期望 404，实际为: %d", respNew.StatusCode)
	}
	_ = respNew.Body.Close()
}

// TestGoroutineStable 验证连续 20 次热重载后 goroutine 数量保持稳定 (±5)
func TestGoroutineStable(t *testing.T) {
	SetSnapshotGracePeriod(20 * time.Millisecond)
	defer SetSnapshotGracePeriod(2 * time.Second)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer upstream.Close()

	tempDir := t.TempDir()
	cfgPath := writeTestConfigFile(t, tempDir, upstream.URL, "/api", 10)

	cm := config.NewConfigManager(cfgPath)
	if err := cm.LoadConfig(cfgPath); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	initialEngine := buildEngineWithConfig(cm.GetConfig())
	initialSnap := &Snapshot{
		Version:   cm.GetVersion(),
		Config:    cm.GetConfig(),
		Engine:    initialEngine,
		CreatedAt: time.Now(),
	}
	dispatcher := NewDispatcher(initialSnap)
	setupReloadLifecycle(cm, dispatcher)

	gwServer := httptest.NewServer(dispatcher)
	defer gwServer.Close()

	client := gwServer.Client()

	// 先运行 2 次预热以初始化 runtime / sync.Pool
	for i := 0; i < 2; i++ {
		writeTestConfigFile(t, tempDir, upstream.URL, "/api", 10+i)
		_ = cm.ReloadConfig()
		resp, _ := client.Get(gwServer.URL + "/api")
		if resp != nil {
			_ = resp.Body.Close()
		}
	}
	time.Sleep(50 * time.Millisecond)
	runtime.GC()

	baselineGoroutines := runtime.NumGoroutine()

	// 执行 20 次连续热重载并发送请求
	for i := 1; i <= 20; i++ {
		writeTestConfigFile(t, tempDir, upstream.URL, "/api", 20+i)
		if err := cm.ReloadConfig(); err != nil {
			t.Fatalf("第 %d 次重载失败: %v", i, err)
		}

		resp, err := client.Get(gwServer.URL + "/api")
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		_ = resp.Body.Close()
	}

	// 等待所有旧快照的延迟释放窗口结束
	time.Sleep(100 * time.Millisecond)
	runtime.GC()

	finalGoroutines := runtime.NumGoroutine()
	diff := math.Abs(float64(finalGoroutines - baselineGoroutines))

	t.Logf("Goroutine 稳定性检测: 基准=%d, 最终=%d, 差值=%.0f", baselineGoroutines, finalGoroutines, diff)
	if diff > 5 {
		t.Errorf("连续重载后 goroutine 数量不稳定，基准: %d, 最终: %d, 差值: %.0f (允许阈值 ±5)",
			baselineGoroutines, finalGoroutines, diff)
	}
}

// TestReloadMetricsAndLogging 验证重载事件指标计数与状态正确性
func TestReloadMetricsAndLogging(t *testing.T) {
	ResetReloadMetrics()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tempDir := t.TempDir()
	cfgPath := writeTestConfigFile(t, tempDir, upstream.URL, "/api", 10)

	cm := config.NewConfigManager(cfgPath)
	if err := cm.LoadConfig(cfgPath); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	dispatcher := NewDispatcher(&Snapshot{
		Version: cm.GetVersion(),
		Config:  cm.GetConfig(),
		Engine:  buildEngineWithConfig(cm.GetConfig()),
	})
	setupReloadLifecycle(cm, dispatcher)

	// 初始指标应全为 0
	m0 := GetReloadMetrics()
	if m0.TotalReloads != 0 || m0.SuccessReloads != 0 || m0.FailedReloads != 0 {
		t.Fatalf("初始指标非零: %+v", m0)
	}

	// 成功重载一次
	writeTestConfigFile(t, tempDir, upstream.URL, "/api", 11)
	if err := cm.ReloadConfig(); err != nil {
		t.Fatalf("重载失败: %v", err)
	}

	m1 := GetReloadMetrics()
	if m1.TotalReloads != 1 || m1.SuccessReloads != 1 || m1.FailedReloads != 0 {
		t.Errorf("成功重载后指标异常: %+v", m1)
	}
	if m1.LastVersion != "2" {
		t.Errorf("期望 LastVersion 为 2，实际为: %s", m1.LastVersion)
	}
	if m1.LastDuration <= 0 {
		t.Errorf("期望 LastDuration > 0，实际为: %v", m1.LastDuration)
	}

	// 注册失败钩子触发失败重载
	cm.AddReloadHook(func(c *config.Config) error {
		return errors.New("hook error for test")
	})

	writeTestConfigFile(t, tempDir, upstream.URL, "/api", 12)
	_ = cm.ReloadConfig()

	m2 := GetReloadMetrics()
	if m2.TotalReloads != 2 || m2.SuccessReloads != 1 || m2.FailedReloads != 1 {
		t.Errorf("失败重载后指标异常: %+v", m2)
	}
	if m2.LastError == "" {
		t.Error("期望 LastError 非空")
	}
}

// TestDispatcherServeHTTPInitializing 验证分发器在快照未就绪时返回 503
func TestDispatcherServeHTTPInitializing(t *testing.T) {
	d := NewDispatcher(nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	d.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("期望状态码 503，实际得到: %d", rec.Code)
	}
	if rec.Body.String() != "Gateway initializing\n" {
		t.Errorf("期望响应 'Gateway initializing\\n'，实际得到: %q", rec.Body.String())
	}
}
