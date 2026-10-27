# 限流插件（rate_limit）

## 一、概述
限流插件用于限制API请求速率，防止系统过载。支持IP、用户、全局等多种限流维度，支持内存和Redis存储。

## 二、设计目标
1. 支持多种限流维度
2. 支持多种限流算法
3. 灵活配置存储后端
4. 支持跳过部分路径
5. 完善的错误处理和日志记录

## 三、流程图
1. 客户端发起请求
2. 插件拦截请求
3. 计算限流Key
4. 判断是否超限
5. 超限则返回错误，否则放行

## 四、配置参数

| 名称                | 数据类型         | 必填 | 默认值         | 描述                         |
|---------------------|----------------|------|----------------|------------------------------|
| requests_per_second | number \| string | 否 | 10             | 每秒放行请求数；`100` / `100.0` / `"100"` 均可 |
| burst               | int \| string  | 否   | 20             | 令牌桶容量（允许的突发量）；`0` 表示不放行任何请求 |
| dimension           | string         | 否   | path           | 限流维度：`path`（按 URL 路径）/ `ip` / `user` / `global` |
| user_header         | string         | 否   | X-User-ID      | `dimension: user` 时用于区分用户的请求头 |
| max_entries         | int            | 否   | 10000          | 令牌桶数量上限，超出按 LRU 淘汰 |
| ip_based            | bool           | 否   | false          | 历史字段，等价于 `dimension: ip`（`dimension` 优先） |
| trusted_proxies     | array of string| 否   | 全局默认       | 解析客户端 IP 时信任的代理列表（`ip` 维度生效） |

> 注意：
> - `requests_per_second` / `burst` 走统一的数值归一化，YAML 里的整数、浮点、字符串都能正确解析（历史版本只认浮点，写 `100` 会静默回落成 10 rps）。
> - 未配置 `dimension` 且未开启 `ip_based` 时保持历史行为：**按 URL 路径限流**（每个路径一个桶，所有客户端共享）。
> - `dimension: user` 在请求未携带 `user_header` 时退化为按客户端 IP 限流，避免退化成全局限流误伤所有用户。
>
> 以下字段在文档中出现但**当前版本尚未实现**，配置后不生效：`storage` / `redis` / `window_size` / `skip_paths` / `error_code` / `error_message`。

## 五、配置示例

#### IP限流
```yaml
- name: rate_limit
  enabled: true
  order: 3
  config:
    dimension: ip
    requests_per_second: 100
    burst: 200
```

#### 用户限流
```yaml
- name: rate_limit
  enabled: true
  order: 3
  config:
    dimension: user
    requests_per_second: 50
    burst: 100
    user_header: X-User-ID
```

#### Redis存储
```yaml
- name: rate_limit
  enabled: true
  order: 3
  config:
    storage: redis
    redis:
      host: localhost
      port: 6379
      password: ""
      db: 0
```

## 六、运行属性
- 插件执行阶段：流量控制阶段
- 插件执行优先级：3

## 七、请求示例
```bash
curl http://localhost:8080/api/users
```

## 八、处理流程
1. 校验配置参数
2. 计算限流Key
3. 获取/创建令牌桶
4. 判断是否超限
5. 超限则返回错误，否则放行

## 九、错误码

| HTTP 状态码 | 出错信息           | 说明                   |
|-------------|--------------------|------------------------|
| 429         | Too Many Requests  | 超过限流阈值           |

## 十、插件配置
在路由或全局plugins中添加`rate_limit`插件即可。 