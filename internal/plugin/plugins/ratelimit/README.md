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
| requests_per_second | float          | 否   | 10             | 每秒请求数限制                |
| burst               | int            | 否   | 20             | 突发请求数限制                |
| dimension           | string         | 否   | ip             | 限流维度：ip/user/global      |
| storage             | string         | 否   | memory         | 存储类型：memory/redis        |
| redis               | object         | 否   | -              | Redis配置                     |
| window_size         | int            | 否   | 60             | 时间窗口大小（秒）            |
| skip_paths          | array of string| 否   | []             | 跳过限流的路径                |
| error_code          | int            | 否   | 429            | 超限时HTTP状态码              |
| error_message       | string         | 否   | Too Many Requests | 超限时错误信息             |

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