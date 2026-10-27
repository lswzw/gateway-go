# PRD：Gateway-Go 生产就绪改造

| 项目 | 内容 |
|---|---|
| 文档版本 | v1.0 |
| 状态 | 待评审 |
| 关联评审 | 2026-10-26 架构与生产就绪度评估 |
| 涉及模块 | `cmd/gateway`、`internal/config`、`internal/router`、`internal/plugin`、`internal/logger`、`internal/errors` |
| 任务拆分 | [TASKS-production-readiness.md](TASKS-production-readiness.md) |

---

## 1. 背景与问题陈述

Gateway-Go 已具备「配置加载 + 路由匹配 + 插件链 + 反向代理」的完整骨架，`go build` / `go vet` 干净，分层与插件权限模型合理。但在架构评审与本机冒烟验证中，确认其**尚不具备承载生产流量**的条件：存在会破坏业务数据的转发缺陷、缺失优雅退出、部分内置插件不可用或为假实现、安全边界可被绕过、无可观测性与任何自动化测试。

评审结论为：**不建议用于生产主链路；修完 P0 可作内网低风险转发；P0+P1 完成后具备正式接入条件。**

### 1.1 已实测确认的缺陷（证据摘要）

| 编号 | 现象 | 复现结果 | 代码位置 |
|---|---|---|---|
| E1 | `log.level=debug` 时请求体/响应体被截断到 4KB | POST 8KB → 上游仅收 4096 且返回 502；GET 100KB → 客户端仅收 4096，静默截断 | `cmd/gateway/main.go` 421–428、578–589 |
| E2 | 无优雅退出，`read/write_timeout` 等配置从未生效 | SIGTERM 后 <400ms 进程消失，在途 4s 请求断开（`http_code=000`） | `cmd/gateway/main.go` 250–253、269–280 |
| E3 | 插件拒绝后响应体被写两次 | 返回 `{"error":"IP 不在白名单中"}{"error":"IP 127.0.0.1 不在白名单中"}`，非法 JSON | `cmd/gateway/main.go` 479–492 |
| E4 | `error` 插件使合法路由返回 404 | 启用后 `/api/x` → 404，上游完全未被访问；`logger` 从未注入 | `internal/plugin/plugins/error/error.go` 31–62 |
| E5 | 访问日志耗时恒为 ~0，info 级别丢失 `level`/`msg` | `cost: 0.000000408`；日志行无级别字段 | `cmd/gateway/main.go` 603–623、`internal/logger/logger.go` 55–60 |
| E6 | IP 白名单可被 `X-Forwarded-For` 绕过 | gin 1.9.1 默认 `trustedCIDRs = 0.0.0.0/0`，伪造 XFF 被原样采纳 | `internal/plugin/plugins/ipwhitelist/ipwhitelist.go` 51–64 |

### 1.2 代码审查确认的结构性问题

- **双路由实现**：`internal/router.Manager.MatchRoute()` 在运行时无调用点，Trie / RouteCache / regex / wildcard / A-B 分桶均为死代码；实际匹配由 `main.go` 线性遍历完成，仅支持 `exact` / `prefix`。
- **热重载非原子**：`LoadConfig` 先替换配置再执行 hook，hook 失败无回滚；每次 reload 对同一插件实例重复 `Init()`，`ratelimit` / `circuitbreaker` 每次新起 goroutine 永不退出；`Stop()` 直接 `close(chan)`，非幂等。
- **插件模型受限**：插件为全局单例，同一插件无法按路由使用不同参数；`plugins.available.order` 为无效配置（链按代码内 `Order()` 排序）。
- **安全实现失真**：`consistency` 的 RSA/ECDSA/Ed25519 分支实为 `hex(sha256(content))`，不校验客户端签名；HMAC 用 `!=` 比较；`interface_auth` 将 token 拼进 URL 路径且 `true`=拒绝。
- **工程能力缺失**：0 个测试文件、无 CI、无 metrics/trace、无请求体上限、无 TLS；依赖停留在 2023 年版本。
- **文档严重超前于实现**：`middleware:` 配置段、`-config` / `--validate-config`、`/health`、TLS、负载均衡、健康检查、连接池等均不存在。

---

## 2. 目标与非目标

### 2.1 目标

1. **正确性**：网关不得改变请求/响应语义；任何日志级别下都不得截断或丢弃字节。
2. **可用性**：进程可优雅退出、可零 5xx 热重载；滚动发布不中断在途请求。
3. **安全性**：来源 IP 不可伪造；签名校验必须真实生效；令牌不得出现在 URL 与日志中。
4. **可观测性**：具备可用的 metrics、健康探针、可关联的访问日志。
5. **可维护性**：单一路由实现、有回归测试与 CI 门禁，P0 缺陷有对应自动化用例。

### 2.2 非目标（本次不做，需在架构文档显式标注「当前不支持」）

- 动态服务发现（xDS / Consul / K8s Endpoints）、多目标负载均衡、主动健康检查。
- 分布式限流与共享熔断状态（多副本下的全局配额）。
- 管理面 API / Web 控制台 / 配置中心集群。
- 鉴权模型重构（JWT/OIDC 自校验），仍维持「委托外部认证服务」。
- 灰度/AB 路由的高级策略（仅修复既有能力的一致性）。

> 说明：上述能力当前**文档中已有描述但代码未实现**，本次一并从文档删除或标注状态，避免运维误判。

---

## 3. 适用范围与前提假设

| 假设 | 说明 |
|---|---|
| 部署形态 | 单机/少量副本，前置 Nginx 或 LB 作为第一层入口（TLS 终止、连接数限制、请求体上限兜底） |
| 流量规模 | 单实例 ≤ 1k QPS、≤ 50 条路由、≤ 20 个上游 |
| 上游协议 | HTTP/1.1，单一静态地址（本次不引入 LB） |
| 兼容性 | 新增配置项一律可选；旧 `config.yaml` 必须能直接启动 |
| 环境 | Linux + systemd；`/tmp/gateway.pid` 改为可配置路径 |

---

## 4. 需求清单

优先级定义：
- **P0（阻断级）**：不修复则不得承载任何生产流量，含数据损坏、请求丢失、安全绕过、插件不可用。
- **P1（上线前强烈建议）**：不修复则不具备可运维性与可回滚性，允许以「前置 Nginx + 缩小功能面」临时规避，但必须在首个迭代内完成。

### 4.1 P0 需求（阻断级，6 条）

#### REQ-P0-01 转发链路不得修改报文内容

- **现状**：debug 级别下用 `io.LimitReader(req.Body, 4096)` 读取后回写 Body；响应侧 `ModifyResponse` 同样只保留 4KB。
- **需求**：
  1. 日志采集只能「观察」，禁止以任何形式回写/替换 `req.Body`、`resp.Body`。
  2. 提供 `log.body_limit`（默认 0 = 不记录）与 `log.body_sha256`（默认开）；超限仅记录 `body_bytes` 与摘要。
  3. 大报文（≥ 1MB）转发路径使用流式 `io.Copy`，不得整体缓冲进内存。
- **验收标准**：
  - `level=debug` 下 POST 8KB / 1MB，上游 `Content-Length` 与实收字节完全一致，无 502。
  - `level=debug` 下 GET 100KB / 10MB，客户端收到字节数与上游声明一致。
  - 日志中出现 `body_bytes` 与 `body_sha256`（前 8 位），不出现完整报文（当 `body_limit=0`）。
  - 回归用例覆盖 debug / info 两个级别。

#### REQ-P0-02 服务器超时与优雅退出落地

- **现状**：`http.Server` 仅设置 `Addr`/`Handler`；`read_timeout` / `write_timeout` / `max_header_bytes` / `graceful_shutdown_timeout` 只做校验不使用；收到 SIGTERM 直接 `return`。
- **需求**：
  1. `Server.Shutdown(ctx)` 接入，超时取 `graceful_shutdown_timeout`；顺序为「停止接受新连接 → 等待在途 → 关闭插件/日志 → 退出」。
  2. `ReadTimeout` / `ReadHeaderTimeout` / `WriteTimeout` / `IdleTimeout` / `MaxHeaderBytes` 全部生效。
  3. `ListenAndServe` 失败不得在 goroutine 中 `log.Fatalf`，须经由 error channel 回到主流程；端口占用时退出码非 0 且日志可读。
  4. 重复 SIGTERM 幂等；PID 文件路径可配置。
- **验收标准**：
  - 在途 4s 请求于 SIGTERM 后仍返回 200；进程退出耗时 ≤ `graceful_shutdown_timeout + 1s`。
  - 上游 sleep 10s + `write_timeout=2s` → 客户端在 2s 左右收到超时，而非挂死。
  - 超 1MB header → 431/400；`kill -TERM` 两次不 panic。
  - 端口被占用时进程退出码 ≠ 0。

#### REQ-P0-03 统一插件错误契约与响应写出

- **现状**：插件既写响应又返回 error，中间件无条件再写 500，导致双写、非法 JSON、状态码错位。
- **需求**：
  1. 契约：**插件写响应则必须返回 nil；不写响应则返回 error**，由网关兜底写 500。
  2. 中间件兜底前判断 `c.Writer.Written()`。
  3. 错误响应统一结构 `{code, message, request_id}`，内容协商支持 JSON（默认）。
  4. 所有内置插件按新契约整改（`ip_whitelist`、`ratelimit`、`circuitbreaker`、`interface_auth`、`consistency`、`cors`、`error`）。
- **验收标准**：
  - 白名单拒绝：状态 403，body 为单个合法 JSON，`curl | jq .` 成功。
  - 插件返回 error 且未写响应：500 + 合法 JSON，日志含 `route`/`plugin`/`request_id`。
  - 任一场景下 `Content-Length` 与实际 body 一致（不出现拼接）。

#### REQ-P0-04 下线 `error` 插件，收敛至网关核心错误出口

- **现状**：`logger` 未注入（空指针风险），`Execute` 内 `ctx.Next()` 会提前触发 `NoRoute`，导致合法路由 404；且插件链无法捕获后续反向代理 502/504 错误。
- **需求**（已决策采纳方案 B 彻底下线）：
  1. 错误处理、结构化格式输出与监控通知统一由网关核心 `respondError` 中间件接管，不再保留链路冗余且无法全链路覆盖的 `error` 插件。
  2. 从 `registerPlugins()`、内置插件目录和参考文档中移除 `error` 插件。
  3. `LoadAvailablePlugins` 中若检测到配置引用了 `error` 插件，启动期给出明确错误与迁移提示（"error plugin has been deprecated; error handling is now unified into gateway core respondError"）。
- **验收标准**：配置中移除 `error` 插件后 `/api/x` 正常代理返 200；上游 500 或网关故障时输出统一结构 `{code, message, request_id}` 且只写一次；`-race` 下 1000 次请求无 panic。

#### REQ-P0-05 访问日志可信

- **现状**：`startTime` 在代理完成后才取，`cost` 恒约为 0；`level=info` 时被刻意抹掉 `level`/`msg` 字段。
- **需求**：
  1. 计时从请求进入 gin 引擎开始，到响应写完结束；记录 `status`、`bytes`、`upstream`、`rt_ms`、`request_id`、`client_ip`。
  2. 恢复 `level` / `msg` 字段；`access log` 独立开关与独立字段集，不依赖 debug 开关。
  3. 上游 5xx 与代理错误在日志中可区分（`error_kind`）。
- **验收标准**：上游 sleep 4s 时日志 `rt_ms ≈ 4000±50`；每行含 `level=info`、`msg=access`；`jq` 可解析；能按 `request_id` 串联请求全链路。

#### REQ-P0-06 客户端 IP 可信解析

- **现状**：`ctx.ClientIP()` 依赖 gin 默认信任 `0.0.0.0/0`，任意 `X-Forwarded-For` 均被采纳，白名单/限流/AB 分桶全部可绕过。
- **需求**：
  1. 新增 `server.trusted_proxies: []string`，启动时调用 `engine.SetTrustedProxies`。
  2. 未配置时视为无代理，`ClientIP` 取 `RemoteAddr`；配置后按最右侧不可信 IP 解析。
  3. 提供统一 `getClientIP(c)` 供所有插件与日志使用，禁止直接调用 `ClientIP()`。
- **验收标准**：无配置时伪造 XFF 仍被白名单拒绝（403）；配置 `10.0.0.0/8` 后按链路正确取值；单测覆盖多值 XFF、私有网段、空值、IPv6。

### 4.2 P1 需求（上线前强烈建议，7 条）

#### REQ-P1-01 上游转发：超时、重试、连接池

- **现状**：每请求 `httputil.NewSingleHostReverseProxy`，无 per-route Transport；`target.timeout` / `target.retries` 仅出现在死代码中。
- **需求**：per-route `http.Transport`（`MaxIdleConns`、`MaxIdleConnsPerHost`、`IdleConnTimeout`、`ResponseHeaderTimeout`）+ `context` 总超时；`retries` 仅对幂等方法与 5xx/连接错误生效，且可关闭；重试与上游失败打点。
- **验收标准**：`timeout=1s` + 上游 sleep 10s → 504 且 ≤1.2s 返回；连接拒绝时按 `retries` 重试后 502，指标计数正确；压测下 keep-alive 复用率 > 90%。

#### REQ-P1-02 热重载原子化与插件生命周期治理

- **现状**：配置先替换后跑 hook 无回滚；reload 重复 `Init()` 造成 goroutine 泄漏；`Stop()` 非幂等；`globalServer.Handler` 无同步替换。
- **需求**：
  1. 构建新快照（routes + plugin chains + handler）→ 校验 → `atomic.Value` 原子发布；旧快照延迟释放。
  2. 插件 reload 语义为 `Stop() → Init()`，或提供可重入 `Reload()`；`Stop()` 幂等。
  3. 从 `available` 移除的插件必须从注册表摘除，路由引用失效则 reload 失败并保留旧快照。
  4. reload 结果（成功/失败/耗时/版本号）暴露为指标与日志。
- **验收标准**：1000 并发下连续 reload 20 次，`-race` 无告警、goroutine 数稳定（±5）、零 5xx；hook 失败时配置版本号不变且旧配置继续生效。

#### REQ-P1-03 路由实现统一与语义补齐

- **现状**：`main.go` 线性匹配与 `router.Manager` 并存；`regex` / `wildcard` / A-B 在运行时不可用；`config.yaml` 中 `/api`(prio 90) 遮蔽 `v1.api.example.com`(prio 80)。
- **需求**：
  1. 单一事实来源：将 `router.Manager`（Trie + 缓存）接回主链路，删除 `main.go` 中的手工匹配。
  2. 匹配语义：`exact` / `prefix` / `regex` / `wildcard` + `host` / `method` / `headers` / `query`；裁决规则为「更具体的 host 优先于泛化 host，其次比 priority」。
  3. 启动期（`-t` 与启动）检测路由遮蔽与重复，输出告警；`regex` 非法直接失败。
  4. 修正 `config/config.yaml` 中错误的路由示例。
- **验收标准**：100 条路由下单次匹配 < 10µs（benchmark）；host 与优先级用例全通过；非法 regex 时 `-t` 报错退出码 1；示例配置下 `v1.api.example.com/api/*` 命中预期上游。

#### REQ-P1-04 可观测性基线（强制双端口隔离）

- **现状**：无 metrics、无 tracing、无 request-id；`/gatewaygo/health` 暴露在业务端口仅返回静态 ok。
- **需求**：
  1. **强制双端口物理分离**：业务端口（`server.port`，如 8080）严格只承载反向代理流量，不注册任何内部管理或监控探针；新增独立管理端口配置 `admin.listen: "127.0.0.1:9090"`，启动独立 HTTP Server 承载管理面请求。
  2. 管理面端点：
     - `/metrics`（Prometheus）：请求数/状态码分布/上游耗时直方图、限流拒绝数、熔断状态与拒绝数、reload 次数与结果、goroutine 数、配置版本。
     - `/healthz`（存活探针，始终 200）。
     - `/readyz`（就绪探针，配置已加载 + 依赖状态检查，未就绪 503）。
  3. 业务端口移除原有 `/gatewaygo/health`，杜绝内网监控指标和健康探针向外网暴露。
  4. 每请求生成或透传 `X-Request-ID`，写入日志、错误体与上游请求头。
- **验收标准**：Prometheus 抓取管理端口 9090 成功且指标命名符合规范；业务端口 8080 请求 `/metrics` 或 `/healthz` 返回 404；触发熔断后 `gateway_circuit_open_total` 递增；`readyz` 在配置加载失败时返回 503；`request_id` 可跨日志检索。

#### REQ-P1-05 安全加固

- **现状**：`consistency` 假验签、非常量时间比较、支持 MD5；`interface_auth` token 入 URL 且布尔语义反转；无请求体上限；无 TLS。
- **需求**：
  1. `consistency`：改为真实校验（`hmac.Equal`；RSA/ECDSA/Ed25519 必须用客户端签名做 `Verify`）；移除 MD5 或标注不安全并默认禁用；nonce 存储有容量上限与周期清理。
  2. `interface_auth`：
     - **标准协议改造**：Token 统一由请求头（`Authorization: Bearer <token>`）向外部认证服务传递，严禁将 Token 拼接到 URL 路径；认证结果默认依据 HTTP 状态码裁决（2xx=通过，401/403=拒绝）。
     - **旧接口兼容配置**：新增 `consumers.auth_mode: http_status | legacy_unauthorized_bool` 配置项（默认 `http_status`），当设置为 `legacy_unauthorized_bool` 时，支持针对历史 PaaS `/unauthorized` 接口以响应体 `"false"` 判定为通过，同时仍强制使用 Header 传递 Token。
     - 认证结果本地缓存 TTL 可配置（默认 0 不缓存）；响应体读取上限 4KB（`io.LimitReader`）。
  3. 新增 `server.max_body_bytes`（默认 1MB），超限返回 413。
  4. TLS 二选一：内置 `tls.cert_file/key_file`，或文档明确「必须由前置反代终止 TLS」。
- **验收标准**：伪造签名请求 100% 401；同一 nonce 重放第二次 400；10MB 上传返回 413；`grep` 全量日志确认 token 不出现；`govulncheck` 无高危。

#### REQ-P1-06 测试与 CI 门禁

- **现状**：0 个测试文件、无 CI；`CLAUDE.md` 中的测试流程无对应产物。
- **需求**：路由匹配、插件链（顺序/短路/缓存）、限流与熔断状态机、配置 reload 的单测；P0 六条缺陷的回归用例；CI 跑 `go build`、`go vet`、`go test -race`、`govulncheck`；核心包覆盖率 ≥ 60%。
- **验收标准**：`go test ./... -race` 全绿；覆盖率报告可查；PR 未过门禁不可合并。

#### REQ-P1-07 依赖治理

- **现状**：gin 1.9.1（2023）、`x/net 0.19`、`x/crypto 0.17`；`go 1.22` + `toolchain go1.24` 混用。
- **需求**：升级 gin 至 1.10+（最新）、`x/net`、`x/crypto`、viper 系列；统一 `go` 指令与 toolchain；`govulncheck` 零 high/critical。
- **验收标准**：构建与全量测试通过；ClientIP 与 redirect 行为回归（与 REQ-P0-06 联动验证）；依赖变更记录在 PR 描述。

---

## 5. 非功能需求

| 类别 | 指标 |
|---|---|
| 性能 | 单实例 1k QPS 下，网关自身开销 p99 < 5ms（不含上游）；内存稳态不随请求数增长（限流桶/熔断器/插件缓存均有容量上限） |
| 可用性 | reload 期间零 5xx；滚动重启零中断；单实例故障不影响前置 LB 摘除 |
| 安全 | 不引入 high/critical CVE；密钥、token、Authorization 头不得落日志 |
| 兼容性 | 新增配置项可选、有默认值；旧配置文件可直接启动 |
| 可运维性 | 关键路径均有日志与指标；回滚 < 2 分钟 |

---

## 6. 上线门槛（Go / No-Go Checklist）

- [ ] P0 六项（REQ-P0-01 ~ 06）全部合并，且通过本地自动化回归脚本验证
- [ ] P1 中 REQ-P1-06（测试与 CI）已完成，GitHub Actions CI 门禁生效（`-race`、构建、govulncheck、覆盖率）
- [ ] `/metrics`、`/healthz`、`/readyz` 已在独立管理端口（9090）接入监控与告警
- [ ] 灰度策略：5% → 25% → 100%，每阶段观察 ≥ 30min（5xx 率、p99、goroutine 数、上游错误率）
- [ ] 回滚预案：保留上一版本二进制 + 配置快照，回滚演练 < 2min 完成
- [ ] 第一层 Nginx/LB 已配置：TLS 终止、`client_max_body_size`、连接数限制、超时兜底、仅转发业务端口（8080）
- [ ] 架构文档已标注「当前不支持」的能力清单（LB / 服务发现 / 分布式限流 / 管理 API）
- [ ] Runbook 就绪：日志位置、指标含义、常见故障（404 全量 / 插件拒绝 / 上游超时）处置步骤

> 任何一项 P0 未完成，均不得进入灰度。

---

## 7. 里程碑与排期（建议）

| 里程碑 | 范围 | 预估 | 出口条件 |
|---|---|---|---|
| M1 | REQ-P0-01 ~ 06 | 3.0 人日 | 内网小流量可接入；P0 回归验证脚本全绿 |
| M2 | REQ-P1-01 / 02 / 06 | 3.5 人日 | 可安全热重载与滚动发布；CI 门禁生效 |
| M3 | REQ-P1-03 / 04 / 05 / 07 | 5.5 人日 | 具备正式上线条件，通过 Go/No-Go |
| 合计 | — | 约 12 人日（含评审与回归验证） | — |

> 估算不含前置 Nginx 配置与监控平台接入（由运维并行推进）。

---

## 8. 风险与应对

| 风险 | 影响 | 应对 |
|---|---|---|
| 路由接回 `router.Manager` 属核心重构 | 可能引入新的匹配回归 | 鉴于当前无存量生产流量，不引入 `legacy\|trie` 双引擎开关；在构建 Snapshot 时建立详尽表驱动用例与离线对账测试，一次性彻底替换并清理 main.go 线性代码 |
| 插件错误契约变更破坏既有自定义插件 | 已有插件行为改变 | 兼容层：若 `Writer.Written()` 为真则忽略返回的 error，并在日志告警 |
| gin 升级改变 `ClientIP` / redirect 行为 | 白名单与重定向异常 | 先补 REQ-P0-06 的用例，再升级（REQ-P1-07 依赖 REQ-P0-06） |
| 热重载原子化改动面大 | 可能影响启动路径 | 先落地快照结构与单测，再切主链路；保留 `-t` 预校验 |
| 排期紧张导致 P1 被砍 | 可运维性不足 | P1 允许延期，但 REQ-P1-06 与 REQ-P1-04 不得低于最小实现（CI + 独立端口三个端点） |

---

## 9. 附录：评审复现记录

```bash
# 编译与静态检查
go build ./... && go vet ./...

# E1 请求体截断（level=debug，路由 /api -> 本地 upstream）
curl -X POST --data-binary @8kb.txt http://127.0.0.1:18080/api/echo
# => {"error":"代理请求失败: ... ContentLength=8000 with Body length 4096"}

# E1 响应体截断
curl -s http://127.0.0.1:18080/api/big | wc -c   # => 4096（上游实际 100000）

# E2 优雅退出
kill -TERM $(cat /tmp/gateway.pid)   # => 400ms 内进程消失，在途请求 http_code=000

# E3 双写
curl -i http://127.0.0.1:18080/api/x
# => 403 + {"error":"IP 不在白名单中"}{"error":"IP 127.0.0.1 不在白名单中"}

# E4 error 插件
# 配置 plugins: ["error"] 后
curl -o /dev/null -w "%{http_code}\n" http://127.0.0.1:18080/api/x   # => 404

# E6 XFF 伪造
curl -H "X-Forwarded-For: 10.1.2.3" http://127.0.0.1:18080/api/x     # => 通过白名单
```
