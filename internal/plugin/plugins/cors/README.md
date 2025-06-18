# 跨域插件（cors）

## 一、概述
跨域插件用于处理跨域资源共享（CORS）请求，支持灵活配置允许的源、方法、头等，保障前端跨域安全。

## 二、设计目标
1. 支持灵活配置允许的源、方法、头
2. 支持预检请求处理
3. 支持暴露自定义响应头
4. 完善的错误处理和日志记录

## 三、流程图
1. 客户端发起跨域请求
2. 插件拦截请求
3. 判断是否为预检请求
4. 校验源、方法、头
5. 设置CORS响应头
6. 放行或拒绝请求

## 四、配置参数

| 名称                | 数据类型         | 必填 | 默认值         | 描述                         |
|---------------------|----------------|------|----------------|------------------------------|
| allowed_origins     | array of string| 否   | ["*"]          | 允许的源                      |
| allowed_methods     | array of string| 否   | ["GET","POST"] | 允许的方法                    |
| allowed_headers     | array of string| 否   | ["*"]          | 允许的请求头                  |
| exposed_headers     | array of string| 否   | ["Content-Length"] | 暴露的响应头              |
| max_age             | string         | 否   | "12h"          | 预检请求缓存时间              |
| allow_credentials   | bool           | 否   | false          | 是否允许携带凭证              |

## 五、配置示例

```yaml
- name: cors
  enabled: true
  order: 5
  config:
    allowed_origins: ["*"]
    allowed_methods: ["GET", "POST", "PUT", "DELETE", "OPTIONS"]
    allowed_headers: ["*"]
    allow_credentials: false
```

## 六、运行属性
- 插件执行阶段：网络阶段
- 插件执行优先级：5

## 七、请求示例
```bash
curl -H "Origin: https://example.com" http://localhost:8080/api/data
```

## 八、处理流程
1. 校验配置参数
2. 判断是否为预检请求
3. 校验源、方法、头
4. 设置CORS响应头
5. 放行或拒绝请求

## 九、错误码

| HTTP 状态码 | 出错信息           | 说明                   |
|-------------|--------------------|------------------------|
| 403         | Forbidden          | 源不被允许             |

## 十、插件配置
在路由或全局plugins中添加`cors`插件即可。 