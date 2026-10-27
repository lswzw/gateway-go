# Gateway-Go 管理面接口与可观测性文档 (REQ-P1-04)

## 1. 概述与强制双端口物理分离

在生产环境中，监控指标、健康检查探针与内部运维端点属于高敏感的管理面能力。为杜绝内网监控指标和健康探针向公网业务暴露，Gateway-Go 强制采用**双端口物理分离架构**：

1. **业务端口（Business Port）**：
   - 由 `server.port` 配置（例如 `8080`），严格只承载业务反向代理流量转发。
   - 原原有业务端口的 `/gatewaygo/health` 端点已彻底移除。
   - 业务端口对 `/metrics`、`/healthz`、`/readyz`、`/gatewaygo/health` 等管理探针请求**严格返回 404**，杜绝管理路由泄露。

2. **管理端口（Admin Port）**：
   - 由 `admin.listen` 配置（默认 `127.0.0.1:9090`），独立启动独立的 HTTP Server 承载管理面端点。
   - 仅绑定在受信任的内网 IP 或本地回环地址上，供 Prometheus 抓取指标及 Kubernetes 存活/就绪探针调用。
   - 在网关收到系统终止信号（SIGINT, SIGTERM）时，管理端口与业务端口一同执行平滑优雅退出（Graceful Shutdown）。

---

## 2. 配置说明

在 `config/config.yaml` 中配置管理面监听地址：

```yaml
# =============================================================================
# 管理面配置部分（强制双端口物理分离 REQ-P1-04）
# =============================================================================
admin:
  listen: "127.0.0.1:9090"        # 管理面监听地址（Prometheus metrics / 探针）
```

若未显式配置 `admin.listen`，默认监听 `127.0.0.1:9090`。

---

## 3. 管理面端点列表

管理端口（默认 `http://127.0.0.1:9090`）提供以下三个标准端点：

### 3.1 Prometheus 指标端点 `/metrics`

- **路径**：`GET /metrics`
- **协议**：Prometheus 文本协议（Exposition Format）
- **状态码**：`200 OK`
- **描述**：导出网关业务指标与 Go 运行时基础指标。指标标签严格使用路由名称（`route`），避免使用未经收敛的 raw URL 作为标签导致指标基数爆炸。

#### 核心业务指标

| 指标名称 | 类型 | 标签 | 含义说明 |
|---|---|---|---|
| `gateway_requests_total` | Counter | `route`, `method`, `code` | 网关处理的 HTTP 请求总数分布 |
| `gateway_upstream_rt_seconds` | Histogram | `route` | 上游转发耗时直方图（桶：`0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10` 秒） |
| `gateway_ratelimit_rejected_total` | Counter | `route` | 限流插件触发限流并拒绝请求的总次数 |
| `gateway_circuit_open_total` | Counter | `route` | 熔断器触发开启并拒绝请求的总次数 |
| `gateway_reload_total` | Counter | `result` | 配置重载次数与结果（`success` / `failure`） |
| `gateway_config_version` | Gauge | `version` | 当前处于生效状态的配置版本 |
| `go_goroutines` | Gauge | 无 | 运行时活跃 goroutine 数量（Prometheus 基础指标） |

#### 请求示例

```bash
curl -s http://127.0.0.1:9090/metrics | grep gateway_
```

---

### 3.2 存活探针 `/healthz` (Liveness Probe)

- **路径**：`GET /healthz`
- **内容类型**：`application/json`
- **状态码**：`200 OK`
- **描述**：供 Kubernetes 存活探针（livenessProbe）或负载均衡健康检查使用，服务存活期间始终返回 200。

#### 响应格式

```json
{
  "status": "ok"
}
```

#### 请求示例

```bash
curl -i http://127.0.0.1:9090/healthz
```

---

### 3.3 就绪探针 `/readyz` (Readiness Probe)

- **路径**：`GET /readyz`
- **内容类型**：`application/json`
- **状态码**：
  - 就绪状态：`200 OK`（配置加载成功且生效）
  - 未就绪状态：`503 Service Unavailable`（配置尚未加载完成或加载失败）
- **描述**：供 Kubernetes 就绪探针（readinessProbe）检测网关实例是否准备好接收业务流量。当实例未就绪时，前置流量负载均衡器不会将请求分发至该网关实例。

#### 成功响应（200 OK）

```json
{
  "status": "ready"
}
```

#### 未就绪响应（503 Service Unavailable）

```json
{
  "status": "unready"
}
```

#### 请求示例

```bash
curl -i http://127.0.0.1:9090/readyz
```

---

## 4. 全链路 Request-ID 规范

为实现微服务全链路追踪与日志检索，网关在请求入口处强制执行 Request-ID 中间件（`response.RequestIDMiddleware`）：

1. **获取或生成**：
   - 检查客户端入站请求头 `X-Request-ID`（以及兼容小写的 `x-request-id`、`X-Request-Id`）。
   - 若客户端已传入 Request-ID，网关原样保留使用；若未传入，网关自动生成高唯一性 RFC 4122 UUID v4 字符串。
2. **上下文存储与回写**：
   - 存入 Gin 上下文：`c.Set("request_id", reqID)`，供后续插件、错误兜底与访问日志调用。
   - 写入客户端响应头：`c.Writer.Header().Set("X-Request-ID", reqID)`。
3. **上游服务透传**：
   - 反向代理向后端上游转发请求时，在 Director 中统一设置 `req.Header.Set("X-Request-ID", reqID)`，确保微服务链路透传。
4. **错误与日志**：
   - 网关错误响应结构体统一包含 `"request_id": "<ID>"` 字段。
   - Access 日志中记录 `request_id` 字段，支持全链路跨日志关联排障。

---

## 5. Kubernetes 部署探针示例

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 9090
  initialDelaySeconds: 3
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /readyz
    port: 9090
  initialDelaySeconds: 3
  periodSeconds: 5
```
