# 错误处理插件（error）

## 一、概述
错误处理插件用于统一处理API错误响应，支持自定义错误页面、响应格式和错误码映射，提升用户体验。

## 二、设计目标
1. 统一错误响应格式
2. 支持自定义错误页面和错误码
3. 支持JSON/HTML响应格式
4. 支持包含堆栈信息
5. 完善的错误处理和日志记录

## 三、流程图
1. 后端服务返回错误
2. 插件拦截错误响应
3. 生成统一错误响应
4. 返回给客户端

## 四、配置参数

| 名称                  | 数据类型         | 必填 | 默认值         | 描述                         |
|-----------------------|----------------|------|----------------|------------------------------|
| error_page_template   | string         | 否   | ""             | 错误页面模板                  |
| error_response_format | string         | 否   | json           | 错误响应格式：json/html       |
| include_stack_trace   | bool           | 否   | false          | 是否包含堆栈信息              |
| error_codes           | object         | 否   | -              | 自定义错误码映射              |

## 五、配置示例

```yaml
- name: error
  enabled: true
  order: 100
  config:
    error_page_template: ""
    error_response_format: json
    include_stack_trace: false
    error_codes:
      400: "Bad Request"
      401: "Unauthorized"
      403: "Forbidden"
      404: "Not Found"
      500: "Internal Server Error"
```

## 六、运行属性
- 插件执行阶段：错误处理阶段
- 插件执行优先级：100

## 七、请求示例
```bash
curl http://localhost:8080/api/unknown
```

## 八、处理流程
1. 校验配置参数
2. 拦截错误响应
3. 生成统一错误响应
4. 返回给客户端

## 九、错误码

| HTTP 状态码 | 出错信息           | 说明                   |
|-------------|--------------------|------------------------|
| 400         | Bad Request        | 参数错误               |
| 401         | Unauthorized       | 未认证                 |
| 403         | Forbidden          | 无权限                 |
| 404         | Not Found          | 资源不存在             |
| 500         | Internal Server Error | 服务内部错误         |

## 十、插件配置
在路由或全局plugins中添加`error`插件即可。 