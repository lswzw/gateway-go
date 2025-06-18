# 限流插件 (Rate Limit Plugin)

限流插件使用令牌桶算法实现请求限流，支持多种限流策略和配置选项。

## 功能特点

- 🚦 基于令牌桶算法的限流
- 🔄 支持突发流量处理
- 👥 支持多维度限流（IP、用户、路径等）
- ⚡ 高性能实现
- 🧹 自动清理过期限流器

## 配置说明

```yaml
plugins:
  - name: rate_limit
    enabled: true
    order: 1
    config:
      # 每秒请求数
      requests_per_second: 100
      
      # 突发流量限制
      burst: 200
      
      # 限流维度（可选）
      dimension: ip  # ip, user, path
```

## 限流算法

### 令牌桶算法

1. 工作原理：
   - 系统以固定速率向桶中放入令牌
   - 请求到达时，需要获取一个令牌
   - 如果桶中没有令牌，请求被限流

2. 参数说明：
   - `requests_per_second`: 令牌生成速率
   - `burst`: 桶的容量，允许的突发流量

## 使用示例

1. 注册插件：
```go
rateLimitPlugin := ratelimit.New()
pluginManager.Register(rateLimitPlugin)
```

2. 配置插件：
```yaml
plugins:
  - name: rate_limit
    enabled: true
    order: 1
    config:
      requests_per_second: 100
      burst: 200
```

3. 限流响应：
```json
{
    "error": "请求过于频繁，请稍后重试"
}
```

## 限流维度

### IP 限流
- 基于客户端 IP 地址
- 支持 X-Forwarded-For 头
- 适用于 API 限流

### 用户限流
- 基于用户 ID
- 从请求头获取用户标识
- 适用于用户级限流

### 路径限流
- 基于请求路径
- 支持通配符匹配
- 适用于特定接口限流

## 性能优化

1. 内存管理
   - 使用 sync.Map 存储限流器
   - 定期清理过期限流器
   - 避免内存泄漏

2. 并发处理
   - 线程安全的限流器实现
   - 最小化锁竞争
   - 高效的令牌计算

## 监控指标

1. 限流统计
   - 总请求数
   - 限流请求数
   - 限流率

2. 性能指标
   - 限流器数量
   - 内存使用量
   - 处理延迟

## 注意事项

1. 配置建议
   - 根据系统容量设置限流阈值
   - 合理设置突发流量限制
   - 考虑多实例部署情况

2. 运维建议
   - 监控限流情况
   - 定期检查限流配置
   - 及时调整限流参数

3. 故障处理
   - 限流器故障降级
   - 配置热更新
   - 异常情况告警 