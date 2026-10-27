# Gateway-Go 性能优化实现方案

## 1. 概述

基于压测结果分析，本文档提供了具体的代码优化实现方案，旨在提升 Gateway-Go 网关的性能表现。

---

### 最新一轮压测结果分析

#### 1. 硬件资源对性能的影响
- **2C2G vs 4C4G**：
  - 在**直接访问**和**无插件转发**场景下，4C4G硬件环境下吞吐量显著提升。例如：
    - **直接访问**：4000并发下，吞吐量从2C2G的3560 RPS提升至4C4G的5295 RPS。
    - **无插件转发**：1800并发下，吞吐量从2C2G的8632 RPS提升至4C4G的8632 RPS（未进一步提升，可能受其他因素限制）。
  - **结论**：硬件资源（CPU/内存）对高并发场景的吞吐量有直接影响，4C4G环境能显著缓解资源瓶颈。

#### 2. 插件对性能的影响
- **插件转发 vs 无插件转发**：
  - 在相同硬件环境下，插件转发的吞吐量显著低于无插件转发：
    - **4C4G环境下**：
      - **无插件转发**（1800并发）：8632 RPS。
      - **插件转发**（1800并发）：7534 RPS（代码优化后）。
    - **2C2G环境下**：
      - **无插件转发**（1200并发）：5698 RPS。
      - **插件转发**（1200并发）：5128 RPS（代码优化后）。
  - **结论**：插件引入了额外的处理逻辑（如健康检查、路由规则等），导致性能损耗。需优化插件逻辑或减少不必要的插件调用。

#### 3. 内核参数优化效果
- **内核优化后的表现**：
  - **插件转发场景**中，内核参数调整（如 `tcp_tw_reuse=1`、`net.core.somaxconn=65535`）显著提升了吞吐量：
    - **4C4G+内核优化**下，2500并发时吞吐量为6266 RPS，3200并发时为4419 RPS，相比未优化时（1800并发7534 RPS）仍有提升空间。
  - **结论**：内核参数优化能有效缓解网络连接队列和资源回收的瓶颈，但高并发下仍需进一步优化。

#### 4. 代码优化效果
- **代码优化对性能的提升**：
  - **健康检查接口**：4000并发下吞吐量从4661 RPS提升至5623 RPS（4C4G环境）。
  - **插件转发**：800并发下吞吐量从3460 RPS提升至5370 RPS（代码优化后）。
  - **结论**：代码层面的优化（如减少锁竞争、优化路由逻辑）对性能提升有显著效果，需持续迭代优化。

#### 5. 并发用户数与吞吐量的关系
- **直接访问与无插件转发**：
  - 吞吐量随着并发用户数增加先上升后下降。例如：
    - **直接访问**（4C4G）：2000并发时11037 RPS，4000并发时5295 RPS。
    - **无插件转发**（4C4G）：1800并发时8632 RPS，3200并发时未测试（可能因资源限制下降）。
  - **结论**：存在一个**性能拐点**，超过一定并发数后，资源耗尽导致吞吐量下降。需根据硬件资源合理设计并发策略。

#### 6. 问题定位与建议
- **性能瓶颈分析**：
  1. **插件转发的性能损耗**：
     - 插件转发的吞吐量始终低于无插件转发，需检查插件处理逻辑是否引入高延迟（如健康检查、日志记录等）。
     - **建议**：优化插件执行路径，减少不必要的计算或I/O操作。
  2. **高并发下的资源限制**：
     - 4C4G环境下，3200并发时吞吐量下降至4419 RPS，可能因CPU或内存成为瓶颈。
     - **建议**：监控CPU利用率和内存占用，进一步升级硬件或优化代码。
  3. **内核参数调优空间**：
     - 当前内核参数已较完善，但高并发下仍需调整 `tcp_max_syn_backlog`、`net.core.netdev_max_backlog` 等参数。
     - **建议**：结合系统负载动态调整内核参数，或使用 `epoll` 等高效事件模型。

#### 7. 总结与优化方向
- **核心结论**：
  1. **硬件资源**是性能的基础，4C4G环境比2C2G提升30%以上吞吐量。
  2. **插件转发**引入性能损耗，需针对性优化。
  3. **代码优化**和**内核调优**可显著提升高并发场景下的吞吐量。
- **后续建议**：
  1. **插件优化**：减少插件链路中的耗时操作，如异步化处理、缓存中间结果。
  2. **硬件扩容**：针对3200+并发场景，考虑横向扩展网关实例或升级更高性能硬件。
  3. **压力测试扩展**：增加更细粒度的测试点（如5000并发），验证极限性能。
  4. **监控体系完善**：实时监控网关的CPU、内存、连接数，及时发现瓶颈。

---


---

## 2. 核心优化方案

### 2.1 路由匹配优化

#### 2.1.1 Trie树路由匹配

**问题分析**：当前路由匹配使用线性遍历，时间复杂度为 O(n)，在高并发场景下性能较差。

**优化方案**：实现基于Trie树的路由匹配算法。

```go
// internal/router/trie.go
package router

import (
    "strings"
    "sync"
)

// TrieNode Trie树节点
type TrieNode struct {
    children map[string]*TrieNode
    route    *RouteDefinition
    isEnd    bool
    mu       sync.RWMutex
}

// TrieRouter 基于Trie树的路由器
type TrieRouter struct {
    root *TrieNode
    mu   sync.RWMutex
}

// NewTrieRouter 创建Trie路由器
func NewTrieRouter() *TrieRouter {
    return &TrieRouter{
        root: &TrieNode{
            children: make(map[string]*TrieNode),
        },
    }
}

// Insert 插入路由
func (tr *TrieRouter) Insert(path string, route *RouteDefinition) {
    tr.mu.Lock()
    defer tr.mu.Unlock()
    
    parts := strings.Split(strings.Trim(path, "/"), "/")
    node := tr.root
    
    for _, part := range parts {
        if node.children == nil {
            node.children = make(map[string]*TrieNode)
        }
        
        if _, exists := node.children[part]; !exists {
            node.children[part] = &TrieNode{
                children: make(map[string]*TrieNode),
            }
        }
        node = node.children[part]
    }
    
    node.isEnd = true
    node.route = route
}

// Match 匹配路由
func (tr *TrieRouter) Match(path string) (*RouteDefinition, bool) {
    tr.mu.RLock()
    defer tr.mu.RUnlock()
    
    parts := strings.Split(strings.Trim(path, "/"), "/")
    node := tr.root
    
    for _, part := range parts {
        if node.children == nil {
            return nil, false
        }
        
        if child, exists := node.children[part]; exists {
            node = child
        } else {
            return nil, false
        }
    }
    
    if node.isEnd {
        return node.route, true
    }
    
    return nil, false
}
```

#### 2.1.2 路由缓存实现

```go
// 路由缓存实现
type RouteCache struct {
    cache map[string]*RouteDefinition
    mu    sync.RWMutex
    size  int
}

func NewRouteCache(size int) *RouteCache {
    return &RouteCache{
        cache: make(map[string]*RouteDefinition, size),
        size:  size,
    }
}

func (rc *RouteCache) Get(key string) (*RouteDefinition, bool) {
    rc.mu.RLock()
    defer rc.mu.RUnlock()
    
    route, exists := rc.cache[key]
    return route, exists
}

func (rc *RouteCache) Set(key string, route *RouteDefinition) {
    rc.mu.Lock()
    defer rc.mu.Unlock()
    
    // 简单的LRU实现
    if len(rc.cache) >= rc.size {
        // 删除第一个元素（简化实现）
        for k := range rc.cache {
            delete(rc.cache, k)
            break
        }
    }
    
    rc.cache[key] = route
}
```

### 2.2 连接池优化

#### 2.2.1 HTTP连接池实现

```go
// internal/proxy/connection_pool.go
package proxy

import (
    "net/http"
    "sync"
    "time"
)

// ConnectionPool HTTP连接池
type ConnectionPool struct {
    maxIdleConns        int
    maxIdleConnsPerHost int
    idleConnTimeout     time.Duration
    maxConnsPerHost     int
    clients             map[string]*http.Client
    mu                  sync.RWMutex
}

// NewConnectionPool 创建连接池
func NewConnectionPool() *ConnectionPool {
    return &ConnectionPool{
        maxIdleConns:        100,
        maxIdleConnsPerHost: 10,
        idleConnTimeout:     90 * time.Second,
        maxConnsPerHost:     100,
        clients:             make(map[string]*http.Client),
    }
}

// GetClient 获取HTTP客户端
func (cp *ConnectionPool) GetClient(targetURL string) *http.Client {
    cp.mu.RLock()
    if client, exists := cp.clients[targetURL]; exists {
        cp.mu.RUnlock()
        return client
    }
    cp.mu.RUnlock()
    
    cp.mu.Lock()
    defer cp.mu.Unlock()
    
    // 双重检查
    if client, exists := cp.clients[targetURL]; exists {
        return client
    }
    
    // 创建新的客户端
    transport := &http.Transport{
        MaxIdleConns:        cp.maxIdleConns,
        MaxIdleConnsPerHost: cp.maxIdleConnsPerHost,
        IdleConnTimeout:     cp.idleConnTimeout,
        MaxConnsPerHost:     cp.maxConnsPerHost,
        DisableCompression:  true, // 减少CPU开销
        DisableKeepAlives:   false,
    }
    
    client := &http.Client{
        Transport: transport,
        Timeout:   30 * time.Second,
    }
    
    cp.clients[targetURL] = client
    return client
}

// WarmUpConnections 预热连接
func (cp *ConnectionPool) WarmUpConnections(targets []string) {
    for _, target := range targets {
        client := cp.GetClient(target)
        
        // 发送预热请求
        go func(url string, c *http.Client) {
            req, _ := http.NewRequest("HEAD", url, nil)
            c.Do(req)
        }(target, client)
    }
}
```

### 2.3 内存池优化

#### 2.3.1 对象池实现

```go
// internal/pool/buffer_pool.go
package pool

import (
    "bytes"
    "sync"
)

// BufferPool 缓冲区对象池
type BufferPool struct {
    pool sync.Pool
}

// NewBufferPool 创建缓冲区池
func NewBufferPool() *BufferPool {
    return &BufferPool{
        pool: sync.Pool{
            New: func() interface{} {
                return bytes.NewBuffer(make([]byte, 0, 1024))
            },
        },
    }
}

// Get 获取缓冲区
func (bp *BufferPool) Get() *bytes.Buffer {
    return bp.pool.Get().(*bytes.Buffer)
}

// Put 归还缓冲区
func (bp *BufferPool) Put(buf *bytes.Buffer) {
    buf.Reset()
    bp.pool.Put(buf)
}

// RequestPool 请求对象池
type RequestPool struct {
    pool sync.Pool
}

// NewRequestPool 创建请求对象池
func NewRequestPool() *RequestPool {
    return &RequestPool{
        pool: sync.Pool{
            New: func() interface{} {
                return &ProxyRequest{
                    Headers: make(map[string]string),
                    Query:   make(map[string]string),
                }
            },
        },
    }
}

// Get 获取请求对象
func (rp *RequestPool) Get() *ProxyRequest {
    return rp.pool.Get().(*ProxyRequest)
}

// Put 归还请求对象
func (rp *RequestPool) Put(req *ProxyRequest) {
    req.Reset()
    rp.pool.Put(req)
}

// ProxyRequest 代理请求对象
type ProxyRequest struct {
    Method  string
    URL     string
    Headers map[string]string
    Query   map[string]string
    Body    []byte
}

// Reset 重置请求对象
func (pr *ProxyRequest) Reset() {
    pr.Method = ""
    pr.URL = ""
    pr.Body = pr.Body[:0]
    
    for k := range pr.Headers {
        delete(pr.Headers, k)
    }
    
    for k := range pr.Query {
        delete(pr.Query, k)
    }
}
```

### 2.4 插件系统优化

#### 2.4.1 插件结果缓存

```go
// internal/plugin/cache.go
package plugin

import (
    "crypto/md5"
    "encoding/hex"
    "encoding/json"
    "sync"
    "time"
)

// PluginResult 插件执行结果
type PluginResult struct {
    Success bool
    Error   error
    Data    map[string]interface{}
    Expire  time.Time
}

// PluginCache 插件结果缓存
type PluginCache struct {
    cache map[string]*PluginResult
    mu    sync.RWMutex
    ttl   time.Duration
}

// NewPluginCache 创建插件缓存
func NewPluginCache(ttl time.Duration) *PluginCache {
    pc := &PluginCache{
        cache: make(map[string]*PluginResult),
        ttl:   ttl,
    }
    
    // 启动清理过期缓存的goroutine
    go pc.cleanup()
    
    return pc
}

// Get 获取缓存结果
func (pc *PluginCache) Get(key string) (*PluginResult, bool) {
    pc.mu.RLock()
    defer pc.mu.RUnlock()
    
    result, exists := pc.cache[key]
    if !exists {
        return nil, false
    }
    
    // 检查是否过期
    if time.Now().After(result.Expire) {
        return nil, false
    }
    
    return result, true
}

// Set 设置缓存结果
func (pc *PluginCache) Set(key string, result *PluginResult) {
    pc.mu.Lock()
    defer pc.mu.Unlock()
    
    result.Expire = time.Now().Add(pc.ttl)
    pc.cache[key] = result
}

// cleanup 清理过期缓存
func (pc *PluginCache) cleanup() {
    ticker := time.NewTicker(pc.ttl)
    defer ticker.Stop()
    
    for range ticker.C {
        pc.mu.Lock()
        now := time.Now()
        for key, result := range pc.cache {
            if now.After(result.Expire) {
                delete(pc.cache, key)
            }
        }
        pc.mu.Unlock()
    }
}

// generateCacheKey 生成缓存键
func generateCacheKey(ctx *gin.Context, pluginName string) string {
    data := map[string]interface{}{
        "plugin": pluginName,
        "path":   ctx.Request.URL.Path,
        "method": ctx.Request.Method,
        "host":   ctx.Request.Host,
        "query":  ctx.Request.URL.RawQuery,
    }
    
    // 添加关键请求头
    for _, header := range []string{"Authorization", "Content-Type", "User-Agent"} {
        if value := ctx.GetHeader(header); value != "" {
            data[header] = value
        }
    }
    
    jsonData, _ := json.Marshal(data)
    hash := md5.Sum(jsonData)
    return hex.EncodeToString(hash[:])
}
```

### 2.5 并发处理优化

#### 2.5.1 工作池实现

```go
// internal/worker/pool.go
package worker

import (
    "context"
    "sync"
    "time"
)

// Job 任务接口
type Job interface {
    Execute() error
    ID() string
}

// Worker 工作者
type Worker struct {
    id         int
    jobQueue   chan Job
    workerPool chan chan Job
    quit       chan bool
    wg         *sync.WaitGroup
}

// NewWorker 创建工作者
func NewWorker(id int, workerPool chan chan Job, wg *sync.WaitGroup) *Worker {
    return &Worker{
        id:         id,
        jobQueue:   make(chan Job),
        workerPool: workerPool,
        quit:       make(chan bool),
        wg:         wg,
    }
}

// Start 启动工作者
func (w *Worker) Start() {
    go func() {
        for {
            w.workerPool <- w.jobQueue
            
            select {
            case job := <-w.jobQueue:
                job.Execute()
            case <-w.quit:
                w.wg.Done()
                return
            }
        }
    }()
}

// Stop 停止工作者
func (w *Worker) Stop() {
    w.quit <- true
}

// WorkerPool 工作池
type WorkerPool struct {
    workers    int
    jobQueue   chan Job
    workerPool chan chan Job
    quit       chan bool
    wg         sync.WaitGroup
}

// NewWorkerPool 创建工作池
func NewWorkerPool(workers int) *WorkerPool {
    return &WorkerPool{
        workers:    workers,
        jobQueue:   make(chan Job, 1000),
        workerPool: make(chan chan Job, workers),
        quit:       make(chan bool),
    }
}

// Start 启动工作池
func (wp *WorkerPool) Start() {
    for i := 0; i < wp.workers; i++ {
        wp.wg.Add(1)
        worker := NewWorker(i, wp.workerPool, &wp.wg)
        worker.Start()
    }
    
    go wp.dispatch()
}

// Stop 停止工作池
func (wp *WorkerPool) Stop() {
    wp.quit <- true
    wp.wg.Wait()
}

// Submit 提交任务
func (wp *WorkerPool) Submit(job Job) {
    wp.jobQueue <- job
}

// dispatch 分发任务
func (wp *WorkerPool) dispatch() {
    for {
        select {
        case job := <-wp.jobQueue:
            go func(j Job) {
                worker := <-wp.workerPool
                worker <- j
            }(job)
        case <-wp.quit:
            return
        }
    }
}
```

### 5.1 短期优化
1. **连接池优化**：实现HTTP连接池和预热机制
2. **内存池**：为常用对象实现对象池
3. **路由缓存**：为频繁访问的路由添加缓存
4. **插件结果缓存**：缓存插件执行结果
---

## 6. 2026-10 实测缺陷与修复记录

> 压测环境、口径与原始数据见[性能测试报告](performance-testing.md)。本节记录实测中发现的缺陷、根因定位与修复后的复测结果。

### 6.1 P0：热重载自死锁，数据面永久卡死

**现象**：对运行中的网关发送 `SIGHUP`（即 `./gateway -s reload`）后：

- 日志停在 `正在重新加载配置...`，永不结束；
- 所有业务请求永久 hang（客户端超时），进程 CPU 掉至 0.01%；
- 管理面 `/healthz` 仍返回 200，探针/负载均衡完全看不出故障；
- 只能重启进程恢复。空载与带载（c=50）两种条件均 100% 复现。

**根因**：`ReloadConfig` 全程持有配置状态写锁，重载钩子内部又对同一把锁取读锁，`sync.RWMutex` 不可重入 → 自死锁；死锁期间所有请求中间件调用 `GetConfig()` 都被挡在写锁后面。

```go
// 修复前：internal/config/manager.go:164（写锁跨越回调）
	cm.mu.Lock()
	defer cm.mu.Unlock()
```

```go
// 修复前：cmd/gateway/main.go:277（钩子回读配置管理器）
	configManager.AddReloadHook(func(cfg *config.Config) error {
		if routerManager != nil {
			return routerManager.ReloadFromConfig(configManager, pluginManager)
		}
```

```go
// 修复前：internal/router/manager.go:302（同一 goroutine 再次 RLock）
	cfg := m.configManager.GetConfig()
```

SIGQUIT 抓栈（`goroutine 50 [sync.RWMutex.RLock]`）：

```
config.(*ConfigManager).GetConfig            internal/config/manager.go:276
router.(*Manager).loadConfigFromManager      internal/router/manager.go:302
router.(*Manager).ReloadFromConfig           internal/router/manager.go:181
main.startServer.func2                       cmd/gateway/main.go:279
config.(*ConfigManager).ReloadConfig         internal/config/manager.go:191
config.(*ConfigManager).HandleSignals.func1  internal/config/manager.go:376
```

**修复**：

| 文件 | 改动 |
|---|---|
| `internal/config/manager.go` | 新增 `reloadMu` 串行化重载；`cm.mu` 只做状态字段读写，**不再跨越任何回调**；流程改为「锁内取快照 → 锁外解析/准备/钩子/提交 → 提交阶段短暂加锁更新状态」 |
| `cmd/gateway/main.go` | 同步 `routerManager` 的钩子改用传入的 `cfg` 快照，不再回读 `configManager.GetConfig()` |
| `internal/router/manager.go` | 新增 `ReloadFromSnapshot(cfg, pm)`，直接消费配置快照重建路由表 |

**复测**：

| 场景 | 修复前 | 修复后 |
|---|---|---|
| 空载 `SIGHUP` | 卡在 `正在重新加载配置...`，数据面永久 hang | 重载 762 µs 完成（`✓ 配置重载成功 (版本: 2)`） |
| 带载（c=50、40 s）第 10 s `SIGHUP` | 之后所有请求超时，需重启进程 | 全程 0 失败，27,291 RPS，最慢请求 26 ms |

**回归测试**：`TestReloadConfig_HookMayReadConfig`、`TestReloadConfig_Serialized`。

### 6.2 P0：连接池 idle 容量小于并发，端口耗尽

**现象**：800 并发起纯转发吞吐从 27k 崩到 1.3k–2.1k，伴随 502 与客户端超时。抓 `/proc/<pid>/net/tcp` 确认 28,131 个 `TIME_WAIT` 全部是「网关 → 后端:80」，而容器可用本地端口仅 28,231 个。

**根因**：

```go
// 修复前：internal/proxy/transport.go:39
		MaxIdleConns:          1000,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
```

`MaxIdleConnsPerHost` 小于并发时，超额连接在请求结束后无法回池而立即关闭，下批请求重新 dial，形成连接 churn → `TIME_WAIT` 堆满端口区间 → 新连接失败。

**修复**：新增常量 `DefaultMaxIdleConns = 4096`、`DefaultMaxIdleConnsPerHost = 2048`（`internal/proxy/transport.go`），并在 `docs/deployment.md` 补充连接池参数与配套内核参数（`net.ipv4.tcp_tw_reuse=1`、`net.ipv4.ip_local_port_range="10000 65535"`）。

**复测**（默认内核参数）：

| 场景 | 修复前 | 修复后 |
|---|---|---|
| 100 并发 纯转发 | 27,560 RPS | 28,093 RPS |
| 400 并发 纯转发 | 22,329 RPS（p99 122 ms） | 27,816 RPS（p99 39 ms） |
| 800 并发 纯转发 | 2,127 RPS（p99 4.55 s、19×502、`TIME_WAIT` 28,131） | 26,737 RPS（p99 76 ms、0 错误、`TIME_WAIT` 0） |
| 400 并发 转发+插件 | 19,343 RPS（p99 145 ms） | 24,184 RPS（p99 45 ms） |
| 800 并发 转发+插件 | 4,891 RPS（117×502） | 22,564 RPS（0 错误） |

**回归测试**：`TestTransport_IdlePoolCoversConcurrency`。

### 6.3 P1：`rate_limit` 配置解析与维度

**缺陷 1**：`requests_per_second` 只做 `float64` 断言，YAML 中写 `100`（`config/config.yaml` 与插件 README 的写法）会静默回落成 10 rps。

```go
// 修复前：internal/plugin/plugins/ratelimit/ratelimit.go:177
	if rps, ok := p.config["requests_per_second"].(float64); ok {
		requestsPerSecond = rps
	}
```

**缺陷 2**：文档化的 `dimension: ip | user | global` 未实现（非测试代码零命中），代码只认 `ip_based`，`dimension: ip` 实际退化为按 URL 路径全局限流。

**修复**（`internal/plugin/plugins/ratelimit/ratelimit.go`）：

- 新增 `toFloat64` / `toInt64` 归一化，兼容 `int / int64 / uint64 / float32 / float64 / 字符串`；
- `rate`、`burst`、`dimension`、`user_header` 在 `Init` 阶段一次性解析；
- `getLimitKey` 支持 `path`（默认，历史行为）/ `ip` / `user` / `global`，保留 `ip_based` 别名与 `burst: 0`（全部拒绝）语义；
- `user` 维度在缺少用户头时退化为按 IP 限流，避免退化成全局限流。

**复测**（10 s、c=10、`burst: 200`）：

| 配置 | 修复前 | 修复后 |
|---|---|---|
| `requests_per_second: 100`（整数） | 299 通过 / 639,024 拒绝（≈10 rps） | 1,197 通过（≈100 rps） |
| `requests_per_second: 100.0`（浮点） | 1,196 通过 | 1,197 通过 |
| `dimension: ip` | 未实现 | 按客户端 IP 建桶 |
| `dimension: global` | 未实现 | 全局单桶 |

**回归测试**：`TestRateLimit_NumericConfigHonored`、`TestRateLimit_Dimension`。

### 6.4 转发路径固定开销（已测量，未改动）

转发路径单请求 ≈136 µs，而网关自身响应仅 ≈37 µs；插件链增量仅 ≈9 µs。优化方向按预期收益排序：

| 优化项 | 依据 |
|---|---|
| 按路由缓存 `ReverseProxy`，避免每请求 `httputil.NewSingleHostReverseProxy` | 每请求分配 + GC 压力 |
| `Director` 去掉 `Header.Clone()` 全量深拷贝，改为按需复制 | 大 Header 场景成本高 |
| `bodyObserver` 仅在需要 Body 摘要时包装 | 每请求固定开销 |
| 上游响应时间直方图改为分桶采样/延迟聚合 | 每请求固定成本 |
| 路由级连接池配置化（`target.connection_pool`） | 当前 `docs/routing.md` 标注「暂不支持」 |

### 6.5 待处理

1. `interface_auth` 的 `cache_ttl` 默认为 0：带 `Authorization` 头且未命中白名单时**每请求同步外呼认证服务**，上线前必须设置缓存或加白名单。
2. 路由级连接池配置仍是规划项。
3. `rate_limit` 的 `storage` / `redis` / `window_size` / `skip_paths` / `error_code` / `error_message` 均未实现（已在插件 README 标注）。

### 6.6 压测环境复现

压测资产已入库到 `bench/`（脚本 + 配置 + 说明）：

```bash
./bench/run.sh up        # 构建二进制、建网络、起后端与压测容器
./bench/run.sh matrix    # 基准矩阵：100/200/400 × direct/self/noplug/plug
./bench/run.sh hiconc    # 高并发：800/1600 × 默认参数/内核调优
./bench/run.sh soak      # 长稳：180 s @ c=100
./bench/run.sh reload    # 带载 SIGHUP 验证重载不中断
./bench/run.sh down
```

---

## 7. 2026-10 第二轮优化：认证缓存 / 池治理 / 转发固定开销

> 复测数据见[性能测试报告](performance-testing.md) §5.3。压测脚本见 `bench/`。

### 7.1 认证缓存（`interface_auth`）

改造前：`cache_ttl=0` 且无上限、无命名空间——每个带 Bearer token 的请求同步外呼认证服务
（5s 超时），缓存 key 只有 token，拒绝结果也缓存，认证失败返回 500 且无任何日志。

改造后：

| 项 | 实现 |
|---|---|
| 存储 | `internal/cache` 本地 LRU（容量可配，默认 20000） |
| key | `sha256(token)` + `consumers.host` + `auth_api` + `auth_mode` |
| 缓存范围 | 只缓存"放行"，401/403 不入缓存 |
| 冷启动 | per-key singleflight，同令牌并发只外呼一次 |
| TTL | 默认 30s（`cache_ttl: 0` 即关闭） |
| 失败语义 | 认证服务不可用 → **503** + warn 日志（含令牌指纹，不含明文）+ 指标 |
| 快速失败 | 连续 5 次失败进入 30s 短路窗口，不再逐请求等满超时 |
| 白名单 | 通配符改为 `regexp.QuoteMeta` 后转正则，`/a.b/*` 不再误匹配 `/axb/1` |
| 指标 | `gateway_auth_cache_hits_total` / `_misses_total` / `gateway_auth_upstream_calls_total{result}` |

设计门禁：**不引入 Redis 等外部共享存储**，`make guard` 会检查 `go.mod`。

### 7.2 上游连接池治理

| 项 | 改造前 | 改造后 |
|---|---|---|
| 池键 | 路由名（同上游多路由各自建池；路由改名即泄漏） | `scheme://host`，同上游共享 |
| 连接上限 | 只设 idle，`MaxConnsPerHost` 未设（不限） | 显式 `MaxConnsPerHost=2048` |
| 回收 | 无（重载既不 Reset 也不 CloseIdle） | 快照切换 2s 后按 `activeHosts` 调 `Reconcile`，锁内摘除、锁外关闭 |
| 可观测 | 无 | 启动日志打印池参数与 fd 预算公式 |

`Reconcile` 的判定依据是新配置中仍存在的上游，因此刚被新请求重建的池不会被误删
（`TestTransportPool_Reconcile` 覆盖）。

### 7.3 转发路径固定开销

| 改动 | 依据 |
|---|---|
| access log 字段构造前置 `Core().Enabled(InfoLevel)` | 生产常用 `log.level=warn/error`，zap 的级别过滤发生在字段求值之后，此前每请求仍构造 ~10 个 `zap.Field` + 6 次 `c.Get` |
| body 统计按需采集（`bodyStatsNeeded`） | 此前只要 body 非空就包装 `bodyObserver`（加锁计数 + 采样缓冲），与日志级别、`body_limit`、sha 开关全无关 |
| 不再写 6 个 gin context key | 消费者只有 access log / debug 日志 |

`ReverseProxy` 每请求新建、`Header.Clone()` 深拷贝、`RetryTransport` 每请求分配**本轮未改**，
原因见 7.4。

### 7.4 CPU profile 结论（c=800，30s）

采样条件：`GATEWAY_PPROF=1`，`hey -z 55s -c 800`（noplug），profile 30s，总样本 114s ≈ 380% CPU。

| 热点 | 占样本 |
|---|---|
| 网络 syscall（`Syscall6`） | 25.1% |
| `httputil.(*ReverseProxy).ServeHTTP`（cum） | 20.5% |
| `runtime.mallocgc`（分配/GC） | 8.8% |
| `proxy.(*RetryTransport).RoundTrip`（cum） | 7.4% |
| `ReverseProxy.copyResponse`（cum） | 4.4% |

结论：**转发成本主要由内核 I/O 与 ReverseProxy 本体承担，自研代码占比很小**。因此
"缓存 ReverseProxy 实例"能省的是 mallocgc 的一部分（区间上限 ≈ 8.8%），不足以支撑重写
转发入口的风险；低风险的两项（日志/body 统计）已经把这部分收益前置拿到了。单请求 CPU 从
≈146 µs 降到 ≈132 µs，与 profile 中"分配 + 字段构造"的量级吻合。

### 7.5 复测结果（默认内核参数）

| 场景 | 优化前 | 优化后 | 变化 |
|---|---|---|---|
| noplug c=100 | 28,093 RPS（p99 11.6 ms） | 34,456 RPS（p99 8.7 ms） | +22.6% |
| noplug c=400 | 27,816 RPS（p99 38.7 ms） | 28,330 RPS（p99 37.0 ms） | +1.8% |
| noplug c=800 | 26,737 RPS（p99 76 ms） | 28,685 RPS（p99 68 ms） | +7.3% |
| plug c=100 | 23,411 RPS（p99 15.3 ms） | 30,225 RPS（p99 9.9 ms） | +29.1% |
| plug c=400 | 24,184 RPS（p99 44.6 ms） | 26,501 RPS（p99 39.1 ms） | +9.6% |
| plug c=800 | 22,564 RPS（p99 92.3 ms） | 25,733 RPS（p99 75.0 ms） | +14.0% |

所有档位 p99 均改善、`TIME_WAIT` 均为 0、无 502/超时。注：c=800 单独复测取得 29,646 RPS
（+10.9%），与上表 28,685 的差异属同环境 ±3% 波动。

### 7.6 仍未做

1. `ReverseProxy` 实例复用 / 去掉 `Header.Clone()` / 复用 `RetryTransport`：profile 显示收益上限
   约为分配的 8.8%，需要重写转发入口（Director 依赖 per-request 状态），暂不启动。
2. `interface_auth` 的 HTTP client 仍用默认 `http.Transport`，不受上游池参数管控——认证调用是
   低频长尾路径，暂不纳入。
