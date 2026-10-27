# interface_auth 外部接口认证插件

## 一、概述

对网关请求做接口级外部认证：白名单路径直接放行，其余请求携带 `Authorization: Bearer <token>`
调用外部认证服务，按其结论放行或拒绝。认证结论在**进程内本地缓存**（可配 TTL），用于消除
"每请求一次外呼"的开销。

## 二、处理流程

```mermaid
graph TD
    A[请求进入网关] --> B{路径在白名单}
    B -- 是 --> C[直接放行]
    B -- 否 --> D{缺少 Bearer token}
    D -- 是 --> E[401]
    D -- 否 --> F{本地缓存命中}
    F -- 是 --> C
    F -- 否 --> G{认证服务处于短路窗口}
    G -- 是 --> H[503]
    G -- 否 --> I[调用认证服务]
    I -- 2xx / legacy "false" --> J[写入缓存并放行]
    I -- 401 / 403 / legacy "true" --> K[401，不缓存]
    I -- 超时 / 5xx / 读体失败 --> H
```

同令牌的并发冷启动请求会被合并成一次外呼（进程内 singleflight），避免缓存未命中时的击穿。

## 三、配置参数

| 名称 | 类型 | 必填 | 默认值 | 描述 |
|---|---|---|---|---|
| `white_interfaces` | array of string | 否 | `[]` | 白名单路径，支持 `*` 通配；不含 `*` 走精确匹配 |
| `consumers.host` | string | 是 | - | 认证服务地址（`host:port` 或含 scheme 的 URL） |
| `consumers.auth_api` | string | 是 | - | 认证服务路径 |
| `consumers.auth_mode` | string | 否 | `http_status` | `http_status`（2xx 通过 / 401、403 拒绝）或 `legacy_unauthorized_bool`（响应体 `"false"` 通过） |
| `cache_ttl` | int | 否 | **30** | 认证结果缓存 TTL（秒），`0` 表示关闭缓存 |
| `cache_max_entries` | int | 否 | **20000** | 缓存条目上限（LRU 淘汰） |
| `consumers.cache_ttl` | int | 否 | - | 按认证服务粒度覆盖 `cache_ttl`，优先级更高 |

`cache_max_entries` 应按 **峰值活跃令牌数 × 1.5** 估算。容量小于活跃令牌数时 LRU 持续抖动，
命中率会趋近于零——此时调大 `cache_ttl` 没有任何收益。

## 四、缓存语义（务必知悉）

1. **纯本地内存**：缓存只存在于当前进程，**多副本之间不共享**。3 副本部署时每个副本各自
   独立缓存、各自外呼。设计门禁明确禁止引入 Redis 等外部共享存储（见 `internal/cache`）。
2. **撤销延迟上界 = `cache_ttl`**：令牌被撤销后，最迟 `cache_ttl` 秒后生效。若合规要求
   秒级撤销，请把 `cache_ttl` 设为 `0`（代价是每请求一次外呼）。
3. **集群外呼量 ≈ 活跃令牌数 ÷ cache_ttl × 副本数**，与 QPS 无关。例：10 万活跃令牌、
   TTL 30s、3 副本 → 约 1 万外呼/秒。上线后请用 `gateway_auth_upstream_calls_total` 核对。
4. **只缓存放行结论**：401/403 不缓存，避免认证服务抖动导致的拒绝在 TTL 内持续生效。

## 五、错误码

| HTTP 状态码 | 出错信息 | 说明 |
|---|---|---|
| 401 | Token missing or invalid | 缺少或无法解析 Bearer token |
| 401 | Forbidden: Access denied | 认证服务判定拒绝（401/403） |
| 401 | response Unknown Type | `legacy_unauthorized_bool` 模式返回未知响应体 |
| 503 | Auth service call failed | 认证服务超时、连接失败、返回非 2xx/401/403 |
| 503 | Auth service temporarily unavailable | 连续失败 5 次后进入 30s 短路窗口 |

> 认证服务不可用返回 **503** 而不是 500：与业务上游 5xx 区分，便于告警与熔断识别；
> 任何情况下都不会降级为"跳过认证放行"。

## 六、可观测性

| 指标 | 说明 |
|---|---|
| `gateway_auth_cache_hits_total{route}` | 本地缓存命中次数 |
| `gateway_auth_cache_misses_total{route}` | 本地缓存未命中次数 |
| `gateway_auth_upstream_calls_total{route,result}` | 真实外呼次数，`result` = `ok` / `deny` / `error` / `open` |

认证服务失败会输出 warn 日志（含 `route_name`、令牌指纹、认证服务地址与失败原因），
**日志中不会出现明文令牌**。

## 七、配置示例

```yaml
plugins:
  available:
    - name: interface_auth
      enabled: true
      order: 900
      config:
        cache_ttl: 30            # 0 = 关闭缓存，每请求外呼
        cache_max_entries: 20000 # ≥ 峰值活跃令牌数 × 1.5
        consumers:
          host: "auth-service.namespace.svc.cluster.local:8080"
          auth_api: "/auth/realms/paas/protocol/openid-connect/unauthorized"
          auth_mode: http_status
        white_interfaces:
          - "/info"
          - "/health"
          - "/verification/*"
```

## 八、运行属性

- 插件执行阶段：认证阶段
- 插件执行优先级：900（在限流、熔断、CORS 之后执行）
