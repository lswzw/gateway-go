# 日志插件（logger）

## 一、概述
日志插件用于记录请求和响应日志，支持结构化日志、采样、缓冲等特性，便于问题追踪和运维监控。

## 二、设计目标
1. 支持结构化日志输出
2. 支持采样和自定义采样规则
3. 支持日志缓冲与批量写入
4. 支持自定义日志字段
5. 支持跳过部分路径
6. 完善的错误处理

## 三、流程图
1. 客户端发起请求
2. 插件拦截请求
3. 判断是否采样/跳过
4. 记录请求相关信息
5. 日志写入缓冲区
6. 定时批量写入日志

## 四、配置参数

| 名称            | 数据类型         | 必填 | 默认值         | 描述                         |
|-----------------|----------------|------|----------------|------------------------------|
| level           | string         | 否   | info           | 日志级别：debug/info/warn/error |
| sample_rate     | float          | 否   | 1.0            | 采样率（0.0-1.0）            |
| log_headers     | bool           | 否   | true           | 是否记录请求头               |
| log_query       | bool           | 否   | true           | 是否记录查询参数             |
| log_body        | bool           | 否   | false          | 是否记录请求体               |
| log_response    | bool           | 否   | false          | 是否记录响应体               |
| buffer_size     | int            | 否   | 1000           | 缓冲区大小                   |
| flush_interval  | int            | 否   | 5              | 刷新间隔（秒）               |
| skip_paths      | array of string| 否   | []             | 跳过的路径                   |
| fields          | object         | 否   | -              | 自定义字段                   |

## 五、配置示例

```yaml
- name: logger
  enabled: true
  order: 1
  config:
    level: info
    sample_rate: 0.1
    log_headers: true
    log_query: true
    buffer_size: 1000
    flush_interval: 5
    skip_paths: ["/health"]
    fields:
      service: gateway-go
      version: 1.0.0
```

## 六、运行属性
- 插件执行阶段：日志记录阶段
- 插件执行优先级：1

## 七、请求示例
```bash
curl http://localhost:8080/api/users
```

## 八、处理流程
1. 校验配置参数
2. 判断是否采样/跳过
3. 记录请求相关信息
4. 日志写入缓冲区
5. 定时批量写入日志

## 九、错误码

| HTTP 状态码 | 出错信息           | 说明                   |
|-------------|--------------------|------------------------|
| -           | -                  | 日志插件不影响业务响应 |

## 十、插件配置
在路由或全局plugins中添加`logger`插件即可。 