# IP白名单插件（ip_whitelist）

## 一、概述
IP白名单插件用于基于客户端IP地址控制访问权限，支持白名单和黑名单两种模式，保障API安全。

## 二、设计目标
1. 支持白名单和黑名单模式
2. 支持灵活配置IP段和单个IP
3. 支持自定义真实IP头
4. 支持可信代理配置
5. 完善的错误处理和日志记录

## 三、流程图
1. 客户端发起请求
2. 插件拦截请求
3. 获取客户端真实IP
4. 判断IP是否在白名单/黑名单
5. 决定是否放行或拒绝

## 四、配置参数

| 名称                | 数据类型         | 必填 | 默认值         | 描述                         |
|---------------------|----------------|------|----------------|------------------------------|
| mode                | string         | 否   | whitelist      | 模式：whitelist/blacklist    |
| ip_whitelist        | array of string| 否   | []             | IP白名单                     |
| ip_blacklist        | array of string| 否   | []             | IP黑名单                     |
| real_ip_header      | string         | 否   | X-Real-IP      | 真实IP头                     |
| forwarded_for_header| string         | 否   | X-Forwarded-For| 转发IP头                     |
| trusted_proxies     | array of string| 否   | []             | 可信代理                     |

## 五、配置示例

#### 白名单模式
```yaml
- name: ip_whitelist
  enabled: true
  order: 10
  config:
    mode: whitelist
    ip_whitelist:
      - "192.168.1.0/24"
      - "10.0.0.0/8"
    real_ip_header: X-Real-IP
```

#### 黑名单模式
```yaml
- name: ip_whitelist
  enabled: true
  order: 10
  config:
    mode: blacklist
    ip_blacklist:
      - "192.168.1.100"
      - "10.0.0.50"
      - "172.16.0.100"
```

## 六、运行属性
- 插件执行阶段：安全控制阶段
- 插件执行优先级：10

## 七、请求示例
```bash
curl http://localhost:8080/api/users
```

## 八、处理流程
1. 校验配置参数
2. 获取客户端真实IP
3. 判断IP是否在白名单/黑名单
4. 决定是否放行或拒绝

## 九、错误码

| HTTP 状态码 | 出错信息           | 说明                   |
|-------------|--------------------|------------------------|
| 403         | Forbidden          | IP不在允许范围         |

## 十、插件配置
在路由或全局plugins中添加`ip_whitelist`插件即可。 