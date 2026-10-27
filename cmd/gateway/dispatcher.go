package main

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"gateway-go/internal/config"
	"gateway-go/internal/logger"
	"gateway-go/internal/plugin"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Snapshot 路由快照结构体 (REQ-P1-02 前半)
type Snapshot struct {
	Version   string
	Config    *config.Config
	Engine    *gin.Engine
	CreatedAt time.Time
	cleanup   func()
}

// Release 释放快照资源
func (s *Snapshot) Release() {
	if s != nil && s.cleanup != nil {
		s.cleanup()
	}
}

// Dispatcher 路由分发器，持有原子快照指针 (REQ-P1-02 前半)
type Dispatcher struct {
	snapshot atomic.Pointer[Snapshot]
}

// NewDispatcher 创建分发器实例
func NewDispatcher(snap *Snapshot) *Dispatcher {
	d := &Dispatcher{}
	if snap != nil {
		d.snapshot.Store(snap)
	}
	return d
}

// ServeHTTP 实现 http.Handler 接口，每次请求只做无锁 atomic.Load (REQ-P1-02)
func (d *Dispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	snap := d.snapshot.Load()
	if snap != nil && snap.Engine != nil {
		snap.Engine.ServeHTTP(w, r)
	} else {
		http.Error(w, "Gateway initializing", http.StatusServiceUnavailable)
	}
}

// Snapshot 获取当前活跃快照
func (d *Dispatcher) Snapshot() *Snapshot {
	return d.snapshot.Load()
}

// Store 原子存储新快照
func (d *Dispatcher) Store(snap *Snapshot) {
	d.snapshot.Store(snap)
}

// Swap 原子替换快照并返回旧快照
func (d *Dispatcher) Swap(snap *Snapshot) *Snapshot {
	return d.snapshot.Swap(snap)
}

// ReloadMetrics 热重载监控指标 (REQ-P1-02)
type ReloadMetrics struct {
	mu             sync.RWMutex
	TotalReloads   int64
	SuccessReloads int64
	FailedReloads  int64
	LastReloadTime time.Time
	LastDuration   time.Duration
	LastVersion    string
	LastError      string
}

// RecordSuccess 记录成功的重载指标
func (rm *ReloadMetrics) RecordSuccess(version string, duration time.Duration) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.TotalReloads++
	rm.SuccessReloads++
	rm.LastReloadTime = time.Now()
	rm.LastDuration = duration
	rm.LastVersion = version
	rm.LastError = ""
}

// RecordFailure 记录失败的重载指标
func (rm *ReloadMetrics) RecordFailure(version string, duration time.Duration, err error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.TotalReloads++
	rm.FailedReloads++
	rm.LastReloadTime = time.Now()
	rm.LastDuration = duration
	rm.LastVersion = version
	if err != nil {
		rm.LastError = err.Error()
	}
}

// Get 获取当前指标快照
func (rm *ReloadMetrics) Get() ReloadMetrics {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return *rm
}

var (
	globalDispatcher    = &Dispatcher{}
	globalReloadMetrics = &ReloadMetrics{}

	snapshotGraceMu     sync.RWMutex
	snapshotGracePeriod = 2 * time.Second
)

// SetSnapshotGracePeriod 设置旧快照延迟释放窗口（供单测加速或生产定制）
func SetSnapshotGracePeriod(d time.Duration) {
	snapshotGraceMu.Lock()
	defer snapshotGraceMu.Unlock()
	snapshotGracePeriod = d
}

// getSnapshotGracePeriod 获取旧快照延迟释放窗口
func getSnapshotGracePeriod() time.Duration {
	snapshotGraceMu.RLock()
	defer snapshotGraceMu.RUnlock()
	return snapshotGracePeriod
}

// GetReloadMetrics 获取全局重载指标
func GetReloadMetrics() ReloadMetrics {
	return globalReloadMetrics.Get()
}

// ResetReloadMetrics 重置全局重载指标（供测试使用）
func ResetReloadMetrics() {
	globalReloadMetrics.mu.Lock()
	defer globalReloadMetrics.mu.Unlock()
	globalReloadMetrics.TotalReloads = 0
	globalReloadMetrics.SuccessReloads = 0
	globalReloadMetrics.FailedReloads = 0
	globalReloadMetrics.LastReloadTime = time.Time{}
	globalReloadMetrics.LastDuration = 0
	globalReloadMetrics.LastVersion = ""
	globalReloadMetrics.LastError = ""
}

// logReloadEvent 记录重载事件日志与指标打点 (REQ-P1-02)
func logReloadEvent(version string, duration time.Duration, success bool, err error) {
	if success {
		globalReloadMetrics.RecordSuccess(version, duration)
		if logger.Log != nil {
			logger.Log.Info("配置重载成功",
				zap.String("version", version),
				zap.Duration("duration", duration),
				zap.String("status", "success"),
			)
		}
	} else {
		globalReloadMetrics.RecordFailure(version, duration, err)
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		if logger.Log != nil {
			logger.Log.Error("配置重载失败",
				zap.String("version", version),
				zap.Duration("duration", duration),
				zap.String("status", "failed"),
				zap.String("error", errStr),
			)
		}
	}
}

// buildSnapshotForConfig 根据给定配置构建独立的路由快照与插件环境 (REQ-P1-02)
func buildSnapshotForConfig(newCfg *config.Config, nextVersion string) (*Snapshot, error) {
	// 1. 创建隔离的插件管理器并注册所有标准插件
	pm := plugin.NewManager()
	if err := registerPluginsTo(pm); err != nil {
		return nil, fmt.Errorf("注册插件失败: %w", err)
	}

	// 2. 加载新配置中的可用插件
	if err := loadAvailablePluginsTo(pm, newCfg); err != nil {
		return nil, fmt.Errorf("加载可用插件失败: %w", err)
	}

	// 3. 加载新配置中的路由插件
	if err := loadRoutePluginsTo(pm, newCfg); err != nil {
		return nil, fmt.Errorf("加载路由插件失败: %w", err)
	}

	// 4. 构建绑定该插件管理器的 gin.Engine
	engine := buildEngineWithConfigAndPlugins(newCfg, pm)

	snap := &Snapshot{
		Version:   nextVersion,
		Config:    newCfg,
		Engine:    engine,
		CreatedAt: time.Now(),
		cleanup: func() {
			_ = pm.StopAll()
		},
	}
	return snap, nil
}

// setupReloadLifecycle 编排配置管理器与分发器的原子重载生命周期 (REQ-P1-02)
func setupReloadLifecycle(cm *config.ConfigManager, dispatcher *Dispatcher) {
	if cm == nil || dispatcher == nil {
		return
	}

	var candidateMu sync.Mutex
	var candidateSnapshot *Snapshot

	cm.SetReloadLifecycle(
		// 1. prepareHook: 构建候选快照（若失败直接中止）
		func(newCfg *config.Config, nextVersion string) error {
			candidateMu.Lock()
			defer candidateMu.Unlock()

			snap, err := buildSnapshotForConfig(newCfg, nextVersion)
			if err != nil {
				return err
			}
			candidateSnapshot = snap
			return nil
		},
		// 2. commitHook: 所有钩子执行成功后，原子切换快照并延迟释放旧快照
		func(newCfg *config.Config, nextVersion string) error {
			candidateMu.Lock()
			snap := candidateSnapshot
			candidateSnapshot = nil
			candidateMu.Unlock()

			if snap == nil {
				return fmt.Errorf("无有效候选快照可供切换")
			}

			// 原子切换快照
			oldSnap := dispatcher.Swap(snap)
			globalEngine = snap.Engine

			// 延迟释放旧快照资源（保留在途请求正常处理）
			if oldSnap != nil {
				go func(old *Snapshot) {
					time.Sleep(getSnapshotGracePeriod())
					old.Release()
				}(oldSnap)
			}
			return nil
		},
		// 3. rollbackHook: 若任何阶段失败，丢弃候选快照并释放资源
		func(newCfg *config.Config, err error) {
			candidateMu.Lock()
			snap := candidateSnapshot
			candidateSnapshot = nil
			candidateMu.Unlock()

			if snap != nil {
				snap.Release()
			}
		},
	)

	// 设置指标与日志监听器
	cm.SetReloadEventListener(func(version string, duration time.Duration, success bool, err error) {
		logReloadEvent(version, duration, success, err)
	})
}
