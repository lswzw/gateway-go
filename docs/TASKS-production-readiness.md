# TASKS：Gateway-Go 生产就绪改造（按 PR 拆分）

> 对应需求文档：[PRD-production-readiness.md](PRD-production-readiness.md)
> 拆分口径：**1 个 PR = 1 个可独立评审、可独立回滚的变更**；单个 PR 预估 ≤ 1.5 人日，超过则继续拆分。

---

## 1. 总览

| PR | 标题 | 需求 | 类型 | 预估 | 依赖 | 风险 |
|---|---|---|---|---|---|---|
| PR-01 | 日志采集不再改写报文，修复 4KB 截断 | REQ-P0-01 | fix | 0.5 | — | 低 |
| PR-02 | 服务器超时落地与优雅退出 | REQ-P0-02 | fix | 0.5 | — | 低 |
| PR-03 | 统一插件错误契约与响应出口 | REQ-P0-03 | refactor | 0.5 | — | 中 |
| PR-04 | 彻底下线 `error` 插件并收敛至统一错误出口 | REQ-P0-04 | chore | 0.5 | PR-03 | 低 |
| PR-05 | 重构访问日志（计时、字段、级别） | REQ-P0-05 | fix | 0.5 | PR-01 | 低 |
| PR-06 | 可信代理与统一 ClientIP 解析 | REQ-P0-06 | fix | 0.5 | — | 中 |
| PR-07 | 上游转发：Transport、超时、重试 | REQ-P1-01 | feat | 1.0 | PR-02 | 中 |
| PR-08 | 路由快照原子热重载 | REQ-P1-02 | refactor | 1.5 | PR-03 | 高 |
| PR-09 | 插件生命周期治理（Init/Stop/摘除） | REQ-P1-02 | refactor | 1.0 | PR-08 | 中 |
| PR-10 | 路由执行统一：接回 `router.Manager` | REQ-P1-03 | refactor | 1.5 | PR-08 | 高 |
| PR-11 | 路由语义补齐与示例配置修正 | REQ-P1-03 | feat | 1.0 | PR-10 | 中 |
| PR-12 | 可观测性：强制双端口 metrics / 探针 / request-id | REQ-P1-04 | feat | 1.0 | PR-01, PR-05 | 低 |
| PR-13 | 安全加固：验签、token 传递、body 上限 | REQ-P1-05 | fix | 1.5 | PR-06, PR-03 | 高 |
| PR-14 | 测试基线与 CI 门禁 | REQ-P1-06 | chore | 1.0 | PR-01~06 | 低 |
| PR-15 | 依赖升级与漏洞扫描 | REQ-P1-07 | chore | 0.5 | PR-06 | 中 |
| | **合计** | | | **13.0 人日** | | |

### 1.1 依赖图

```
PR-01 ──┐
        ├─> PR-05 ──┐
PR-03 ──┴─> PR-04   │
                    ├─> PR-12 (强制双端口管理面)
PR-02 ──> PR-07     │
PR-06 ──> PR-13 <───┘（PR-13 另依赖 PR-03）
PR-08 ──> PR-09
PR-08 ──> PR-10 ──> PR-11
PR-06 ──> PR-15
P0 阶段（PR-01~06）通过本地测试套件与脚本验证；
P1 阶段实施 PR-14（CI 先行），为 P1 后续模块提供全自动持续集成门禁。
```

### 1.2 交付批次

| 批次 | 包含 PR | 目标 | 验证口径 |
|---|---|---|---|
| 批次 1（P0，约 3.0 人日） | PR-01 ~ PR-06 | 解决阻断级缺陷，具备内网小流量可用性 | 本地测试脚本与 curl 回归验证 |
| 批次 2（P1 前半，约 4.5 人日） | PR-14、PR-07、PR-08、PR-09 | 建立 CI 门禁，具备安全热重载与滚动发布能力 | CI 门禁生效 + 并发压测 reload |
| 批次 3（P1 后半，约 5.5 人日） | PR-10 ~ PR-13、PR-15 | 满足上线 Go/No-Go，具备双端口监控与安全加固 | 全量 CI + 漏洞扫描 + 监控抓取验证 |

---

## 2. P0 任务

### PR-01 日志采集不再改写报文，修复 4KB 截断

- **需求**：REQ-P0-01
- **类型 / 预估**：fix / 0.5 人日
- **改动文件**：`cmd/gateway/main.go`、`internal/config/config.go`、`internal/config/validator.go`
- **实现要点**：
  1. 删除 `io.ReadAll(io.LimitReader(req.Body, 4096))` + 回写 `req.Body` 的整段逻辑；请求体记录改为可选采样（`log.body_limit`，默认 0 不记录），仅记录 `body_bytes` 与 `body_sha256` 前 8 位。
  2. 响应侧删除 `ModifyResponse` 中 `resp.Body = io.NopCloser(bytes.NewBuffer(respBody))` 的回写；需要记录时用 `io.TeeReader` 旁路采样，采样后立即丢弃，不影响主流程。
  3. 新增配置项并在校验器中校验范围（`body_limit` ∈ [0, 65536]）。
  4. 大报文转发保持流式，不引入全量缓冲。
- **验收标准**：
  - `level=debug` 下 POST 8KB / 1MB，上游 `Content-Length` 与实收一致，无 502。
  - `level=debug` 下 GET 100KB / 10MB，客户端收齐。
  - 日志含 `body_bytes`、`body_sha256`，不含完整报文。
- **测试**：新增 `main` 侧集成测试（`httptest` + 假上游），表驱动覆盖 debug/info × 4 种报文尺寸。
- **风险 / 回滚**：纯删除有害逻辑，回滚即恢复原分支；无可观测性副作用（仅减少日志内容）。
- **审查重点**：确认无任何 `req.Body` / `resp.Body` 赋值语句残留。

---

### PR-02 服务器超时落地与优雅退出

- **需求**：REQ-P0-02
- **类型 / 预估**：fix / 0.5 人日
- **改动文件**：`cmd/gateway/main.go`、`internal/config/config.go`
- **实现要点**：
  1. `http.Server` 补齐 `ReadTimeout`、`ReadHeaderTimeout`（取 `read_timeout` 的 1/2 或独立配置）、`WriteTimeout`、`IdleTimeout`、`MaxHeaderBytes`。
  2. 抽出 `runServer(ctx)`：`ListenAndServe` 错误写入 `errCh`；主流程 `select { <-quit; <-errCh }`。
  3. 收到 SIGTERM → `srv.Shutdown(ctx, graceful)` → `pluginManager.StopAll()` → `logger.Sync()` → 退出码 0；超时强制 `srv.Close()` 并退出码 1。
  4. 移除 goroutine 中的 `log.Fatalf`；两次 SIGTERM 幂等（`sync.Once`）。
  5. PID 文件路径改为 `server.pid_file`（默认 `/tmp/gateway-go-<port>.pid`）。
- **验收标准**：在途 4s 请求在 SIGTERM 后返回 200；退出耗时 ≤ `graceful+1s`；`write_timeout=2s` + 上游 sleep 10s → 客户端约 2s 得到超时；1MB header → 4xx；端口占用退出码非 0。
- **测试**：`TestGracefulShutdown`（起 server + 慢 handler + 发信号，断言响应完整）、`TestTimeoutApplied`（断言 `srv.ReadTimeout` 等字段等于配置）。
- **风险 / 回滚**：systemd `Restart=always` 下行为变化需在 staging 验证停服时间；回滚即还原 `main.go`。
- **审查重点**：确认不存在 `log.Fatalf` 于非 main goroutine。

---

### PR-03 统一插件错误契约与响应出口

- **需求**：REQ-P0-03
- **类型 / 预估**：refactor / 0.5 人日
- **改动文件**：`cmd/gateway/main.go`、`internal/plugin/core/interface.go`、全部 `internal/plugin/plugins/*`
- **实现要点**：
  1. 在 `core` 包注释中固化契约：写响应 → 返回 `nil`；不写响应 → 返回 `error`。
  2. 新增统一出口 `respondError(c, code, msg, requestID)`，输出 `{code,message,request_id}`；中间件仅在 `!c.Writer.Written()` 时调用。
  3. 逐个整改插件：`ip_whitelist`（写 403 → 返回 nil）、`ratelimit`（写 429 → 返回 nil）、`circuitbreaker`（写 503 → 返回 nil）、`interface_auth`、`consistency`、`cors`、`error`。
  4. 插件返回 error 时记录结构化日志（`route`、`plugin`、`error`）。
- **验收标准**：白名单拒绝 body 为单个合法 JSON；未写响应的 error 场景兜底 500 且合法；`Content-Length` 与实际一致。
- **测试**：表驱动用例覆盖「插件写响应」「插件返回 error」「插件 panic（Recovery 兜底）」三类。
- **风险 / 回滚**：契约变更影响自定义插件；建议按 PRD 第 8 节加兼容层（`Written()` 为真时忽略 error 并 warn）。
- **审查重点**：确认无插件同时「写响应 + 返回 error」。

---

### PR-04 彻底下线 `error` 插件并收敛至统一错误出口

- **需求**：REQ-P0-04
- **类型 / 预估**：chore / 0.5 人日
- **改动文件**：`cmd/gateway/main.go`、`internal/plugin/plugins/error/`（删除）、`internal/plugin/manager.go`、`docs/plugins/reference.md`
- **实现要点**：
  1. 彻底移除 `internal/plugin/plugins/error` 插件及其目录，从 `cmd/gateway/main.go` 的 `registerPlugins()` 中摘除。
  2. 错误处理与响应格式化全量由 PR-03 建立的网关核心中间件 `respondError` 接管。
  3. 在 `LoadAvailablePlugins` 与配置校验中增加拦截：若配置文件中仍配置了 `error` 插件，启动期抛出明确警告或错误并给出迁移指引。
  4. 从文档 `docs/plugins/reference.md` 中删除 `error` 插件说明，并在变更日志中记录下线说明。
- **验收标准**：配置中移除 `error` 插件后，`/api/x` 正常代理返 200；上游 500 时由网关兜底输出标准 `{code, message, request_id}` 响应且只写一次；`-race` 下 1000 次请求无 panic。
- **测试**：`TestGatewayErrorHandlingWithoutErrorPlugin`（上游 500、未知路由 404、插件拒绝等场景格式断言）。
- **审查重点**：确认无任何 `error` 插件的注册与导入残留，统一错误出口接管完全。

---

### PR-05 重构访问日志（计时、字段、级别）

- **需求**：REQ-P0-05
- **类型 / 预估**：fix / 0.5 人日
- **改动文件**：`cmd/gateway/main.go`、`internal/logger/logger.go`、`internal/config/config.go`
- **实现要点**：
  1. 用独立的 gin 中间件在链路最外层记录 `startTime`，`defer` 中输出 access log（`status`、`bytes`、`upstream`、`rt_ms`、`request_id`、`client_ip`、`error_kind`）。
  2. 删除在代理之后创建 `bodyLogWriter` 再 `c.Next()` 的无效逻辑。
  3. `logger.Init` 中移除「info 级别抹掉 `level`/`msg`」的分支；如需精简体积，改为 `log.access_fields` 白名单。
  4. `access log` 与 debug 开关解耦（`log.access: true|false`）。
- **验收标准**：上游 sleep 4s → `rt_ms ≈ 4000±50`；每行含 `level`、`msg=access`；`jq` 可解析；`rt_ms` 与 Prometheus 直方图量级一致。
- **测试**：固定延迟假上游断言 `rt_ms` 区间；断言 info 级别日志含 `level` 字段。
- **风险 / 回滚**：日志字段变更影响既有采集规则；需同步通知运维更新解析配置（写入 PR 描述）。
- **审查重点**：计时起点是否在插件链之前。

---

### PR-06 可信代理与统一 ClientIP 解析

- **需求**：REQ-P0-06
- **类型 / 预估**：fix / 0.5 人日
- **改动文件**：`cmd/gateway/main.go`、`internal/config/config.go`、`internal/config/validator.go`、`internal/plugin/plugins/ipwhitelist/ipwhitelist.go`、`internal/plugin/plugins/ratelimit/ratelimit.go`
- **实现要点**：
  1. 新增 `server.trusted_proxies: []string`（CIDR），启动时 `engine.SetTrustedProxies`；为空时传 `nil` 并显式关闭 `ForwardedByClientIP`（或直接取 `RemoteAddr`）。
  2. 提供 `internal/netutil.ClientIP(c)` 并全量替换直接调用 `ctx.ClientIP()` 的位置。
  3. 校验器校验 CIDR 合法性，非法启动失败。
- **验收标准**：无配置时伪造 `X-Forwarded-For` 仍 403；配置 `10.0.0.0/8` 后按链路取值；IPv6 与多值 XFF 正确。
- **测试**：表驱动（无代理 / 单跳可信 / 多跳 / 伪造 / IPv6 / 空值）。
- **风险 / 回滚**：若现网确实经过代理但未配置，会导致所有请求 IP 变为代理 IP → **必须在 PR 描述中给出当前环境的代理网段并同步配置**。
- **审查重点**：替换是否覆盖全部调用点（`grep -rn "ClientIP()"`）。

---

## 3. P1 任务

### PR-07 上游转发：Transport、超时、重试

- **需求**：REQ-P1-01
- **类型 / 预估**：feat / 1.0 人日
- **依赖**：PR-02（需要正确的 server 超时语义作为对照）
- **改动文件**：`cmd/gateway/main.go`、`internal/proxy/`（新建，或 `internal/router`）、`internal/config/config.go`
- **实现要点**：
  1. 按 route 构建并缓存 `httputil.ReverseProxy` + 专属 `http.Transport`（`MaxIdleConns`、`MaxIdleConnsPerHost`、`IdleConnTimeout`、`ResponseHeaderTimeout`、`TLSHandshakeTimeout`）。
  2. `context.WithTimeout` 施加 `target.timeout`（毫秒），超时返回 504 并标记 `error_kind=timeout`。
  3. 重试：仅 `GET/HEAD/PUT/DELETE/OPTIONS` 与 5xx/连接类错误，最多 `target.retries` 次；重试前重置 body（不支持不可重放 body）。
  4. 修掉 `Director` 中 `req.Header = c.Request.Header` 的别名赋值。
  5. 打点：`upstream_rt`、`retry_total`、`upstream_error_total{kind}`。
- **验收标准**：`timeout=1s` + 上游 sleep 10s → 504 且 ≤1.2s 返回；连接拒绝时按 retries 重试后 502 且计数正确；压测 keep-alive 复用率 > 90%。
- **测试**：`TestUpstreamTimeout`、`TestRetryOn5xx`、`TestNoRetryOnPost`、`TestTransportReuse`（连接计数）。
- **风险 / 回滚**：重试可能放大上游压力 → 默认 `retries=0`，文档说明幂等性要求。
- **审查重点**：重试是否仅在幂等方法与可判定错误下触发。

---

### PR-08 路由快照原子热重载

- **需求**：REQ-P1-02（前半）
- **类型 / 预估**：refactor / 1.5 人日
- **依赖**：PR-03（统一出口后才能安全切换 handler）
- **改动文件**：`cmd/gateway/main.go`、`internal/config/manager.go`、`internal/router/manager.go`
- **实现要点**：
  1. 定义 `Snapshot{routes, chains, engine}`，用 `atomic.Value` 发布；请求侧只做 `Load()`。
  2. reload 流程改为：解析 → 校验 → 构建新快照（含插件链）→ 原子发布 → `old.Stop()`（延迟 1 个请求窗口）。
  3. `ConfigManager.ReloadConfig` 增加失败回滚：hook 失败则不切换版本号，保留旧快照并返回错误。
  4. 移除 `globalServer.Handler = globalEngine` 的直接赋值，改为 `Handler` 指向固定 dispatcher，由 dispatcher 内部 `Load()` 快照。
  5. reload 结果指标（次数/结果/耗时）与日志。
- **验收标准**：1000 并发下连续 reload 20 次，`-race` 无告警；goroutine 数稳定（±5）；零 5xx；hook 失败时版本号不变。
- **测试**：`TestReloadAtomic`（并发压测 + reload）、`TestReloadHookFailureKeepsOld`、`TestGoroutineStable`。
- **风险 / 回滚**：改动启动与重载主路径 → 充分覆盖高并发热重载单测，线下压测验证零 5xx 后合入。
- **审查重点**：旧快照释放时机是否可能被在途请求引用（需保证 `Stop` 延后）。

---

### PR-09 插件生命周期治理（Init / Stop / 摘除）

- **需求**：REQ-P1-02（后半）
- **类型 / 预估**：refactor / 1.0 人日
- **依赖**：PR-08
- **改动文件**：`internal/plugin/manager.go`、`internal/plugin/lifecycle.go`、`internal/plugin/plugins/ratelimit/ratelimit.go`、`internal/plugin/plugins/circuitbreaker/circuitbreaker.go`
- **实现要点**：
  1. `Init` 幂等：重复 `Init` 先停止旧清理协程；或统一走 `Reload()`。
  2. `Stop()` 用 `sync.Once` 包裹 `close(stopChan)`，重复调用不 panic。
  3. 从 `available` 移除的插件从 `availablePlugins` 摘除并 `Stop()`；路由仍引用时 reload 失败并保留旧快照。
  4. 限流桶 / 熔断器 / 插件缓存增加容量上限（`max_entries`），LRU 淘汰，避免内存无界增长。
  5. 插件实例注册改为 `name -> factory`，为后续「同插件多配置」留扩展点（本次不实现多实例，仅在注释中说明）。
- **验收标准**：连续 reload 20 次后 goroutine 数不增长；插件被移除后配置引用它时 reload 报错且旧配置生效；10 万不同 path 请求后内存稳定（限流桶有上限）。
- **测试**：`TestPluginInitIdempotent`、`TestPluginStopTwice`、`TestPluginRemovedOnReload`、`TestRateLimitBucketCap`。
- **审查重点**：所有 `close(chan)` 是否都有 `Once` 保护。

---

### PR-10 路由执行统一：接回 `router.Manager`

- **需求**：REQ-P1-03（前半）
- **类型 / 预估**：refactor / 1.5 人日
- **依赖**：PR-08
- **改动文件**：`cmd/gateway/main.go`、`internal/router/manager.go`、`internal/router/trie.go`、`internal/router/cache.go`、`internal/router/router.go`（删除死代码）
- **实现要点**：
  1. 删除 `main.go` 的匿名中间件与 `matchRoute`，改为在 `dispatcher` 中调用 `Manager.MatchRoute`，彻底移除旧线性遍历代码。
  2. `Manager` 只负责「匹配 + 交给 proxy」，不再执行插件链（职责分离，插件链由 dispatcher 调用）。
  3. `TrieRouter.Match` 支持回退到父节点（前缀匹配语义），修正当前只精确命中末节点的问题。
  4. `RouteCache` 增加 TTL 与容量上限；键包含 Method/Host 等多维度；`-race` 下并发读写安全。
  5. 删除 `RegisterRoutes` 中 `TODO` 段落与重复 `/health` 注册。
- **验收标准**：单次路由匹配 < 10µs/op（benchmark 100 条路由）；精确匹配、前缀匹配与兜底路由均命中预期上游；`-race` 通过。
- **测试**：`TestRouterManagerMatch`（表驱动 50+ 组覆盖各类路由）、`BenchmarkMatch100Routes`。
- **风险 / 回滚**：核心重构 → 离线跑全量路径对比测试，无误后直接切入，保持代码整洁。
- **审查重点**：前缀语义、优先级裁决、缓存与热重载的一致性。

---

### PR-11 路由语义补齐与示例配置修正

- **需求**：REQ-P1-03（后半）
- **类型 / 预估**：feat / 1.0 人日
- **依赖**：PR-10
- **改动文件**：`internal/router/manager.go`、`internal/config/validator.go`、`config/config.yaml`、`docs/routing.md`
- **实现要点**：
  1. 补齐 `regex` / `wildcard` / `ab_test` 并接入执行路径；`MatchRegex` 在加载期预编译。
  2. 裁决规则：host 精确 > host 通配 > 无 host，其次 priority 降序，最后 path 长度降序（写入文档）。
  3. 启动与 `-t` 时检测「被遮蔽的路由」并 warn（含遮蔽者与被遮蔽者名称）。
  4. 修正 `config.yaml`：`/api` 不带 host 却 priority 90 遮蔽 `v1.api.example.com`（80）；调整优先级并按 host 拆分示例。
  5. 从文档中删除未实现的字段（`strip_prefix`、`load_balancer`、`health_check`、`connection_pool` 等），或在表格中标注「暂不支持」。
- **验收标准**：`v1.api.example.com/api/*` 命中预期上游；非法 regex 时 `-t` 退出码 1；被遮蔽路由在启动日志中可见；文档字段与结构体一一对应。
- **测试**：路由裁决表驱动用例 ≥ 30 组（含 host/优先级/regex/wildcard 组合）。
- **审查重点**：裁决规则是否与文档完全一致。

---

### PR-12 可观测性：强制双端口 metrics / 探针 / request-id

- **需求**：REQ-P1-04
- **类型 / 预估**：feat / 1.0 人日
- **依赖**：PR-01、PR-05
- **改动文件**：`cmd/gateway/main.go`、`internal/config/config.go`、`internal/metrics/`（新建）、`go.mod`、`docs/api/admin.md`
- **实现要点**：
  1. **强制双端口物理分离**：新增 `admin.listen: "127.0.0.1:9090"` 配置（支持独立 IP/端口），在 `main.go` 中独立启动 Admin HTTP Server。
  2. 业务端口（8080）移除 `/gatewaygo/health` 等任何管理端点，严格只用于反向代理流量转发。
  3. 管理端口（9090）注册端点：
     - `/metrics`（Prometheus）：`gateway_requests_total{route,code}`、`gateway_upstream_rt_seconds`（直方图）、`gateway_ratelimit_rejected_total`、`gateway_circuit_open_total`、`gateway_reload_total{result}`、`gateway_config_version`、`go_goroutines`。
     - `/healthz`（存活探针，始终 200）。
     - `/readyz`（就绪探针，配置已加载且可用，未就绪 503）。
  4. request-id 中间件：透传或生成 `X-Request-ID`，写入 `gin.Context`、下游请求头与日志。
- **验收标准**：Prometheus 抓取 9090 成功；业务端口 8080 请求 `/metrics` 或 `/healthz` 返回 404；触发熔断后对应计数递增；`readyz` 在配置加载失败时 503；日志可按 `request_id` 检索。
- **测试**：`TestAdminServerEndpoints`、`TestBusinessPortNoAdminLeaks`、`TestReadyzUnhealthy`、`TestRequestIDPropagation`。
- **审查重点**：指标基数（避免把 path 原始值作为 label）、业务端口无管理路由泄露。

---

### PR-13 安全加固：验签、token 传递、body 上限

- **需求**：REQ-P1-05
- **类型 / 预估**：fix / 1.5 人日
- **依赖**：PR-06、PR-03
- **改动文件**：`internal/plugin/plugins/consistency/consistency.go`、`internal/plugin/plugins/interface_auth/interface_auth.go`、`cmd/gateway/main.go`、`internal/config/config.go`、`docs/plugins/reference.md`
- **实现要点**：
  1. `consistency`：`hmac.Equal` 替换 `!=`；RSA/ECDSA/Ed25519 改为对 `X-Signature` 做真实验签；移除 MD5 分支（或标记 `insecure` 并默认拒绝）；nonce 存储加容量上限 + 周期清理（独立 ticker）。
  2. `interface_auth`：
     - **标准协议改造**：Token 统一从 `Authorization: Bearer <token>` 请求头传递至外部认证服务（严禁 URL 路径拼接）。认证结果默认以 HTTP 状态码 2xx 视为通过，401/403 视为拒绝。
     - **旧接口兼容配置**：支持配置 `consumers.auth_mode: http_status | legacy_unauthorized_bool`（默认 `http_status`）。当设为 `legacy_unauthorized_bool` 时，支持针对历史 PaaS 接口读取响应体 `"false"` 判为通过，同时仍使用 Header 传递 Token。
     - 缓存 TTL 可配（默认 0=不缓存）；响应体读取上限 4KB（`io.LimitReader`）。
  3. 新增 `server.max_body_bytes`（默认 1MB），在 dispatcher 入口以 `http.MaxBytesReader` 实施，超限 413。
  4. TLS：实现 `server.tls.cert_file/key_file` 时用 `ListenAndServeTLS`；否则在文档明确「必须由前置反代终止 TLS」。
- **验收标准**：伪造签名 100% 401；nonce 重放第二次 400；10MB 上传 → 413；全量日志 `grep -i authorization` 无 token 明文；`govulncheck` 无高危。
- **测试**：`TestVerifyHMACFail`、`TestVerifyRSA`、`TestNonceReplay`、`TestMaxBody`、`TestTokenInHeaderOnly`。
- **审查重点**：不存在「只算 hash 不验签」的分支残留；URL 中无 token。

---

### PR-14 测试基线与 CI 门禁

- **需求**：REQ-P1-06
- **类型 / 预估**：chore / 1.0 人日
- **依赖**：PR-01~06（P0 完成后，在 P1 阶段最先实施）
- **改动文件**：`.github/workflows/ci.yml`（或对应 CI 配置）、`*_test.go`（各包）、`Makefile`（可选）、`CLAUDE.md`
- **实现要点**：
  1. 单测骨架与集成工具库：提供统一的 `httptest` 假上游与断言工具库；覆盖 `internal/router`（匹配/优先级/缓存）、`internal/plugin/chain`（顺序/短路/缓存）、`ratelimit`（令牌桶边界）、`circuitbreaker`（三态迁移）、`internal/config`（reload 成功/失败）。
  2. P0 六条缺陷的自动化回归用例固化为 CI 门禁套件。
  3. CI 流水线：`go build ./...`、`go vet ./...`、`go test ./... -race -coverprofile`、`govulncheck ./...`；覆盖率门槛核心包 ≥ 60%。
  4. 修正 `CLAUDE.md` 中与实现不符的命令（`go test` 目标、`-c` 参数、`/gatewaygo/health`）。
- **验收标准**：CI 全绿；故意引入 P0-01 回归时 CI 失败；覆盖率报告可在 CI 产物中查看。
- **测试**：CI 自身即验证。
- **审查重点**：`-race` 是否真的启用（而非仅本地）。

---

### PR-15 依赖升级与漏洞扫描

- **需求**：REQ-P1-07
- **类型 / 预估**：chore / 0.5 人日
- **依赖**：PR-06（ClientIP 用例先行，避免升级后行为漂移无感知）
- **改动文件**：`go.mod`、`go.sum`
- **实现要点**：
  1. gin 升级到 1.10+ / 最新稳定版；`golang.org/x/net`、`x/crypto`、`viper` 系列同步升级。
  2. 统一 `go` 指令与 `toolchain`（当前 `go 1.22` + `toolchain go1.24`）。
  3. `govulncheck ./...` 纳入 CI（与 PR-14 联动）。
  4. 升级后回归：`ClientIP` 取值、`RedirectTrailingSlash`、`NoRoute` 行为、`ResponseWriter` 接口。
- **验收标准**：构建与全量测试通过；`govulncheck` 无 high/critical；REQ-P0-06 用例仍绿。
- **风险 / 回滚**：gin 大版本可能改动默认配置 → 升级与行为回归合并在同一 PR，问题即回滚 PR。
- **审查重点**：`go.mod` 中是否残留已弃用的间接依赖。

---

## 4. 每个 PR 的统一 DoD

1. 有对应需求的验收标准全部满足，并在 PR 描述中逐条勾选。
2. 新增/修改逻辑附带单测；`go test ./... -race` 通过。
3. 无新增 `TODO` / 死代码；相关文档（`docs/`、`CLAUDE.md`、`config.yaml` 注释）同步更新。
4. 配置新增项有默认值、有校验、有注释，保证旧配置可直接启动。
5. 涉及行为变更时，PR 描述包含「影响面 / 回滚方式 / 需运维配合事项」。
6. 涉及日志或指标字段变更时，同步通知运维更新采集解析规则。
