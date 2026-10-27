# 路由系统

## 概述

Gateway-Go 的路由系统负责将下游客户端请求高效、精准地匹配并转发至对应的上游目标服务。系统以 `router.Manager`（基于 Trie 树索引、多维缓存与标准仲裁层级）作为单一事实来源，支持精确匹配、前缀匹配、正则匹配、Glob 通配符匹配及 A/B 测试动态分流。

---

## 路由匹配与裁决机制

### 匹配类型 (Match Types)

路由系统支持四种标准路径匹配类型：

#### 1. 精确匹配 (exact)

完全匹配请求路径，适用于特定的固定 API 端点。末尾斜杠（Trailing Slash）自动兼容规范化。

```yaml
routes:
  - name: health-check
    match:
      type: exact
      path: /gatewaygo/health
    target:
      url: http://health-service:8080
```

**匹配示例**：
- 请求路径：`/gatewaygo/health` ✅ 匹配
- 请求路径：`/gatewaygo/health/` ✅ 匹配（兼容末尾斜杠）
- 请求路径：`/gatewaygo/health/status` ❌ 不匹配

#### 2. 前缀匹配 (prefix)

匹配路径前缀，路径边界严格以 `/` 分隔（例如 `/api` 匹配 `/api/v1`，但不会误匹配 `/apikey`）。

```yaml
routes:
  - name: api-v1
    match:
      type: prefix
      path: /api/v1
    target:
      url: http://api-v1-service:8080
```

**匹配示例**：
- 请求路径：`/api/v1/users` ✅ 匹配
- 请求路径：`/api/v1/orders/123` ✅ 匹配
- 请求路径：`/api/v1` ✅ 匹配
- 请求路径：`/api/v10/users` ❌ 不匹配（边界隔离）

#### 3. 正则匹配 (regex)

使用正则表达式匹配路径。

> [!IMPORTANT]
> **预编译与合法性强校验**：所有正则路由在配置加载期均执行预编译 (`regexp.Compile`)。若正则表达式语法非法，配置验证将直接失败，执行配置语法测试（`gateway -t`）时将以**退出码 1** 异常退出，防止非法规则进入生产运行态。

```yaml
routes:
  - name: user-api
    match:
      type: regex
      path: "^/api/users/(\\d+)$"
    target:
      url: http://user-service:8080
```

**匹配示例**：
- 请求路径：`/api/users/123` ✅ 匹配
- 请求路径：`/api/users/abc` ❌ 不匹配
- 请求路径：`/api/users/123/profile` ❌ 不匹配

#### 4. 通配符匹配 (wildcard)

支持标准 Glob 路径通配符语法并在配置加载期自动预编译为高性能匹配机：
- `*`：**单段通配**，匹配单个路径段内的任意字符，不跨越路径分隔符 `/`（如 `/api/*/users`、`/documents/*`）。
- `**`：**多段通配**，递归跨段匹配任意多级子路径（如 `/assets/**`）。

```yaml
routes:
  # 单段通配示例
  - name: user-reviews
    match:
      type: wildcard
      path: "/products/*/reviews"
    target:
      url: http://product-service:8080

  # 多段静态资源通配示例
  - name: static-assets
    match:
      type: wildcard
      path: "/assets/**"
    target:
      url: http://static-service:8080
```

**匹配示例**：
- 请求路径：`/products/keyboard/reviews` ✅ 匹配（单段）
- 请求路径：`/products/hardware/mouse/reviews` ❌ 不匹配（单段 `*` 不跨越多个 `/`）
- 请求路径：`/assets/css/theme/dark.css` ✅ 匹配（多段 `**` 递归匹配）
- 请求路径：`/assets/logo.png` ✅ 匹配

---

### A/B 测试与灰度分流 (ab_test)

路由支持内置 A/B 测试动态分流能力：

```yaml
routes:
  - name: payment-experiment
    match:
      type: exact
      path: /pay/checkout
      ab_test:
        enabled: true
        bucket_by: "header:X-User-ID" # 分桶依据: header:<Key>, cookie:<Key>, query:<Key>, 或 ip
        weight_a: 80                 # 权重模式: 80% 流量至 Target A
        weight_b: 20                 # 20% 流量至 Target B
        group_a_target: "http://pay-v1:8080"
        group_b_target: "http://pay-v2:8080"
    target:
      url: http://pay-v1:8080        # 默认回退上游
```

分桶 Key 按 `bucket_by` 提取；若未指定则依次回退至 `X-Request-ID` -> `X-User-ID` -> `ClientIP`。

---

### 路由标准裁决顺序 (Arbitration Rules)

当一个请求同时满足多条候选路由规则时，网关严格遵循以下**三级标准裁决顺序**选出唯一生效路由 (REQ-P1-03)：

```
第一级：Host 作用域层级 (Host Tier)
  Host 精确匹配 (Exact Host) > Host 通配符匹配 (*.domain.com) > 无 Host 限制 (Generic)
        │
        ▼ (当 Host 层级相同时)
第二级：路由优先级降序 (Route Priority Descending)
  Priority 数值从大到小 (如 Priority 200 > Priority 100 > Priority 50)
        │
        ▼ (当 Host 层级与 Priority 均相同时)
第三级：路径特异性与长度降序 (Path Specificity & Length)
  1. 精确匹配 (exact) > 正则/通配符/前缀匹配
  2. 路径长度降序 (较长、较深路径优先，如 /api/v1/orders 胜过 /api/v1)
  3. 携带特定 Headers/QueryParams 条件过滤的路由优先于无条件路由
```

> [!NOTE]
> **Host 层级绝对优先原则**：
> 精确 Host 匹配始终具有最高仲裁权。即使无 Host 的泛化路由设置了更高的 Priority（例如 `Priority: 90`），面对特定 Host（如 `v1.api.example.com`，`Priority: 80`）的请求时，具体 Host 路由依然必定胜出。

---

### 路由遮蔽检测 (Route Shadowing Detection)

当较泛化的路由（如无 Host、路径为 `/api`、优先级 `90`）与较具体的路由（如 `/api/v1`、优先级 `80`）发生范围重叠，且泛化路由具有**更高或相同**的优先级时，具体路由将被完全遮蔽（Shadowed），永远无法被请求命中。

网关在**系统启动期**以及运行**配置语法检查（`gateway -t`）**时，会自动进行路由遮蔽静态分析：
- 检测到遮蔽时，将打印明确的告警日志（包括遮蔽路由名称、被遮蔽路由名称以及冲突原因）；
- 提醒运维和开发人员调整配置，消除潜在配置隐患。

---

## 路由配置规范详解

### 生产环境标准配置示例

```yaml
routes:
  # 1. 监控健康检查路由 (精确匹配，高优先级)
  - name: health-check
    match:
      type: exact
      path: /gatewaygo/health
      priority: 100
    target:
      url: http://127.0.0.1:8080
      timeout: 5000
      retries: 2
    plugins: []

  # 2. 专有 Host 业务路由 (精确 Host 优先)
  - name: v1-api-host
    match:
      type: prefix
      path: /api
      host: "v1.api.example.com"
      priority: 90
    target:
      url: http://v1-api-service:8080
      timeout: 30000
      retries: 3
    plugins: ["interface_auth"]

  # 3. 通配符 Host 业务路由
  - name: internal-services
    match:
      type: prefix
      path: /api
      host: "*.service.internal"
      priority: 85
    target:
      url: http://internal-gateway:8080
      timeout: 30000
      retries: 3
    plugins: []

  # 4. 通用兜底 API 路由 (无 Host 限制，较低优先级)
  - name: generic-api
    match:
      type: prefix
      path: /api
      priority: 80
    target:
      url: http://172.17.0.2:80
      timeout: 30000
      retries: 3
    plugins: ["rate_limit"]

  # 5. 根路径默认服务
  - name: default-service
    match:
      type: exact
      path: /
      priority: 0
    target:
      url: internal://default
    response:
      status: 200
      content: "gateway-go is running"
      content_type: "text/plain"
```

---

### 配置字段对照表

#### 匹配规则 (`match`)

| 字段 | 类型 | 是否必填 | 默认值 | 说明 |
|---|---|---|---|---|
| `type` | string | 是 | `prefix` | 匹配类型：`exact`、`prefix`、`regex`、`wildcard` |
| `path` | string | 是 | - | 匹配路径或通配符表达式（`regex` 模式下可直接作为正则表达式） |
| `regex` | string | 否 | `""` | 正则表达式（`type: regex` 时若配置则优先使用） |
| `host` | string | 否 | `""` | 主机名匹配，支持精确域名（`api.example.com`）与通配符（`*.example.com`） |
| `priority` | int | 否 | `0` | 路由优先级，同 Host 层级下数值越大优先级越高（建议范围 0-1000） |
| `method` | string | 否 | `""` | HTTP 请求方法匹配，多方法逗号分隔，如 `"GET,POST"` |
| `headers` | map | 否 | `{}` | 请求头精确匹配键值对，全部匹配时方可命中 |
| `query_params` | map | 否 | `{}` | 查询参数精确匹配键值对 |
| `ab_test` | object | 否 | - | A/B 测试动态分流配置 |

#### 目标配置 (`target`)

| 字段 | 类型 | 是否必填 | 默认值 | 说明 |
|---|---|---|---|---|
| `url` | string | 是 | - | 后端目标服务地址（如 `http://backend:8080` 或内部服务 `internal://default`） |
| `timeout` | int | 否 | `30000` | 后端请求超时时间（毫秒） |
| `retries` | int | 否 | `3` | 后端请求失败重试次数（仅对幂等方法生效） |

#### 响应配置 (`response`)

| 字段 | 类型 | 是否必填 | 默认值 | 说明 |
|---|---|---|---|---|
| `status` | int | 否 | `200` | 响应状态码（针对 `internal://default` 等内部处理标识） |
| `content` | string | 否 | `""` | 响应内容体 |
| `content_type` | string | 否 | `"text/plain"` | 响应媒体类型 |

---

### 字段支持状态说明（能力边界清单）

根据生产就绪（Production Readiness）架构规范，以下曾出现在历史草案中的非核心功能字段在当前核心中**暂不支持**，如配置将被忽略或应移除：

| 历史字段 | 类别 | 当前支持状态 | 说明与替代方案 |
|---|---|---|---|
| `strip_prefix` | 路由高级 | **暂不支持** | 当前网关按原请求路径转发；如需路径改写需由上游或前置反代处理 |
| `preserve_host` | 路由高级 | **暂不支持** | 统一反向代理透传 Host 头 |
| `path_rewrite` | 请求转发 | **暂不支持** | 路径重写当前暂不支持 |
| `load_balancer` | 负载均衡 | **暂不支持** | 当前每条路由配置单一目标 `target.url`，多目标集群负载均衡由前置 Kubernetes Service/Nginx/SLB 负责 |
| `health_check` | 目标检测 | **暂不支持** | 上游主动健康探测计划在后续版本支持；当前通过重试与熔断机制保证可用性 |
| `connection_pool` | 连接池 | **暂不支持** | 传输连接池已由底层 `http.Transport` 全局与 per-route 统一管控，无需在路由中单独配置 |
| `error_handling` | 错误降级 | **暂不支持** | 统一由 `circuit_breaker` 插件与核心 `respondError` 规范接管 |
| `custom_matcher` | 脚本匹配 | **暂不支持** | 路由匹配统一由原生 Trie 与高性能正则引擎负责 |
| `transform` | 响应转换 | **暂不支持** | 响应体转换暂不支持 |

---

## 运维与测试指南

### 配置语法测试 (`-t`)

在修改路由配置文件后，建议在生产重载前使用 `-t` 参数执行语法校验与遮蔽静态检查：

```bash
# 测试配置文件语法及路由遮蔽
./gateway -t -c ./config/config.yaml
```

若配置文件中存在语法错误或非法的正则表达式，命令将输出具体错误位置并退出（退出码 1）；若存在路由遮蔽隐患，将输出 `⚠ 警告` 提示。

### 热重载路由配置 (`-s reload`)

```bash
# 向运行中的网关发送平滑重载信号
./gateway -s reload
```
网关采用原子快照（Atomic Snapshot）机制完成路由表的无锁原子替换，正在处理中的请求不受影响。
