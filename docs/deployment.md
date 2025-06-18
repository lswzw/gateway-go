# Gateway-Go 部署文档

## 部署概述

Gateway-Go 支持多种部署方式，包括本地部署、Docker 容器化部署、Kubernetes 集群部署等。本文档详细介绍了各种部署方式的配置和步骤。

## 系统要求

### 最低要求
- **CPU**: 1 核心
- **内存**: 512MB
- **磁盘**: 1GB 可用空间
- **网络**: 支持 HTTP/HTTPS 协议

### 推荐配置
- **CPU**: 2-4 核心
- **内存**: 2-4GB
- **磁盘**: 10GB 可用空间
- **网络**: 千兆网络连接

## 本地部署

### 1. 二进制部署

#### 步骤 1: 下载二进制文件

```bash
# 从 GitHub Releases 下载
wget https://github.com/your-org/gateway-go/releases/latest/download/gateway-go-linux-amd64
chmod +x gateway-go-linux-amd64
```

#### 步骤 2: 创建配置文件

```bash
mkdir -p /etc/gateway-go
cat > /etc/gateway-go/config.yaml << EOF
server:
  port: 8080
  mode: release
  read_timeout: "60s"
  write_timeout: "60s"

log:
  level: info
  format: json
  output: /var/log/gateway-go/gateway.log

middleware:
  logger:
    enabled: true
  rate_limit:
    enabled: true
    requests_per_second: 100
  circuit_breaker:
    enabled: true
    failure_threshold: 5

routes:
  - name: api-service
    match:
      type: prefix
      path: /api
    target:
      url: http://backend:8080
      timeout: 30000
      retries: 3
    plugins: ["auth", "rate_limit"]
EOF
```

#### 步骤 3: 创建系统服务

```bash
cat > /etc/systemd/system/gateway-go.service << EOF
[Unit]
Description=Gateway-Go API Gateway
After=network.target

[Service]
Type=simple
User=gateway
Group=gateway
WorkingDirectory=/opt/gateway-go
ExecStart=/opt/gateway-go/gateway-go -config /etc/gateway-go/config.yaml
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
```

#### 步骤 4: 启动服务

```bash
# 创建用户和目录
sudo useradd -r -s /bin/false gateway
sudo mkdir -p /opt/gateway-go /var/log/gateway-go
sudo chown gateway:gateway /opt/gateway-go /var/log/gateway-go

# 复制二进制文件
sudo cp gateway-go-linux-amd64 /opt/gateway-go/gateway-go

# 启动服务
sudo systemctl daemon-reload
sudo systemctl enable gateway-go
sudo systemctl start gateway-go

# 检查状态
sudo systemctl status gateway-go
```

### 2. 源码编译部署

#### 步骤 1: 克隆源码

```bash
git clone https://github.com/your-org/gateway-go.git
cd gateway-go
```

#### 步骤 2: 编译

```bash
# 设置环境变量
export CGO_ENABLED=0
export GOOS=linux
export GOARCH=amd64

# 编译
go build -ldflags="-s -w" -o bin/gateway cmd/gateway/main.go
```

#### 步骤 3: 部署

```bash
# 复制到目标目录
sudo cp bin/gateway /opt/gateway-go/gateway-go
sudo chown gateway:gateway /opt/gateway-go/gateway-go
```

## Docker 部署

### 1. 使用官方镜像

#### 步骤 1: 拉取镜像

```bash
docker pull your-org/gateway-go:latest
```

#### 步骤 2: 创建配置文件

```bash
mkdir -p /opt/gateway-go/config
cat > /opt/gateway-go/config/config.yaml << EOF
server:
  port: 8080
  mode: release

log:
  level: info
  format: json

middleware:
  logger:
    enabled: true
  rate_limit:
    enabled: true
    requests_per_second: 100

routes:
  - name: api-service
    match:
      type: prefix
      path: /api
    target:
      url: http://backend:8080
      timeout: 30000
      retries: 3
EOF
```

#### 步骤 3: 运行容器

```bash
docker run -d \
  --name gateway-go \
  --restart unless-stopped \
  -p 8080:8080 \
  -v /opt/gateway-go/config:/app/config \
  -v /opt/gateway-go/logs:/app/logs \
  your-org/gateway-go:latest
```

### 2. 自定义 Dockerfile

#### 创建 Dockerfile

```dockerfile
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY . .

RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o gateway cmd/gateway/main.go

FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata
WORKDIR /root/

COPY --from=builder /app/gateway .
COPY --from=builder /app/config ./config

EXPOSE 8080

CMD ["./gateway"]
```

#### 构建和运行

```bash
# 构建镜像
docker build -t gateway-go:latest .

# 运行容器
docker run -d \
  --name gateway-go \
  -p 8080:8080 \
  -v $(pwd)/config:/root/config \
  gateway-go:latest
```

### 3. Docker Compose 部署

#### 创建 docker-compose.yml

```yaml
version: '3.8'

services:
  gateway-go:
    image: your-org/gateway-go:latest
    container_name: gateway-go
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./config:/app/config
      - ./logs:/app/logs
    environment:
      - GATEWAY_SERVER_PORT=8080
      - GATEWAY_SERVER_MODE=release
    networks:
      - gateway-network

  backend:
    image: your-backend:latest
    container_name: backend
    restart: unless-stopped
    ports:
      - "8081:8080"
    networks:
      - gateway-network

networks:
  gateway-network:
    driver: bridge
```

#### 启动服务

```bash
docker-compose up -d
```

## Kubernetes 部署

### 1. 创建 ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: gateway-go-config
  namespace: default
data:
  config.yaml: |
    server:
      port: 8080
      mode: release
      read_timeout: "60s"
      write_timeout: "60s"
    
    log:
      level: info
      format: json
    
    middleware:
      logger:
        enabled: true
      rate_limit:
        enabled: true
        requests_per_second: 100
      circuit_breaker:
        enabled: true
        failure_threshold: 5
    
    routes:
      - name: api-service
        match:
          type: prefix
          path: /api
        target:
          url: http://backend-service:8080
          timeout: 30000
          retries: 3
        plugins: ["auth", "rate_limit"]
```

### 2. 创建 Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gateway-go
  namespace: default
  labels:
    app: gateway-go
spec:
  replicas: 3
  selector:
    matchLabels:
      app: gateway-go
  template:
    metadata:
      labels:
        app: gateway-go
    spec:
      containers:
      - name: gateway-go
        image: your-org/gateway-go:latest
        ports:
        - containerPort: 8080
        volumeMounts:
        - name: config
          mountPath: /app/config
        resources:
          requests:
            memory: "256Mi"
            cpu: "250m"
          limits:
            memory: "512Mi"
            cpu: "500m"
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
      volumes:
      - name: config
        configMap:
          name: gateway-go-config
```

### 3. 创建 Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: gateway-go-service
  namespace: default
spec:
  selector:
    app: gateway-go
  ports:
  - protocol: TCP
    port: 80
    targetPort: 8080
  type: ClusterIP
```

### 4. 创建 Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: gateway-go-ingress
  namespace: default
  annotations:
    nginx.ingress.kubernetes.io/rewrite-target: /
spec:
  rules:
  - host: gateway.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: gateway-go-service
            port:
              number: 80
```

### 5. 部署到集群

```bash
# 应用配置
kubectl apply -f configmap.yaml
kubectl apply -f deployment.yaml
kubectl apply -f service.yaml
kubectl apply -f ingress.yaml

# 检查部署状态
kubectl get pods -l app=gateway-go
kubectl get services -l app=gateway-go
```

## 高可用部署

### 1. 负载均衡配置

#### Nginx 负载均衡

```nginx
upstream gateway_backend {
    least_conn;
    server gateway-1:8080 weight=1 max_fails=3 fail_timeout=30s;
    server gateway-2:8080 weight=1 max_fails=3 fail_timeout=30s;
    server gateway-3:8080 weight=1 max_fails=3 fail_timeout=30s;
}

server {
    listen 80;
    server_name gateway.example.com;

    location / {
        proxy_pass http://gateway_backend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        proxy_connect_timeout 30s;
        proxy_send_timeout 30s;
        proxy_read_timeout 30s;
    }
}
```

#### HAProxy 负载均衡

```haproxy
global
    daemon
    maxconn 4096

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend gateway_frontend
    bind *:80
    default_backend gateway_backend

backend gateway_backend
    balance roundrobin
    option httpchk GET /health
    server gateway-1 gateway-1:8080 check
    server gateway-2 gateway-2:8080 check
    server gateway-3 gateway-3:8080 check
```

### 2. 数据库配置

### 3. 监控配置

## 安全配置

### 1. TLS/SSL 配置

#### 生成证书

```bash
# 生成自签名证书
openssl req -x509 -newkey rsa:4096 -keyout key.pem -out cert.pem -days 365 -nodes

# 或使用 Let's Encrypt
certbot certonly --standalone -d gateway.example.com
```

#### 配置 HTTPS

```yaml
server:
  port: 443
  tls:
    enabled: true
    cert_file: /etc/ssl/certs/gateway.crt
    key_file: /etc/ssl/private/gateway.key
```

### 2. 防火墙配置

```bash
# UFW 配置
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable

# iptables 配置
sudo iptables -A INPUT -p tcp --dport 22 -j ACCEPT
sudo iptables -A INPUT -p tcp --dport 80 -j ACCEPT
sudo iptables -A INPUT -p tcp --dport 443 -j ACCEPT
sudo iptables -A INPUT -j DROP
```

### 3. 访问控制

```yaml
middleware:
  ipwhitelist:
    enabled: true
    allowed_ips:
      - "192.168.1.0/24"
      - "10.0.0.0/8"
  
  auth:
    enabled: true
    type: "jwt"
    jwt:
      secret: "your-secret-key"
      expire: "24h"
```

## 性能优化

### 1. 系统调优

#### 内核参数优化

```bash
# /etc/sysctl.conf
net.core.somaxconn = 65535
net.core.netdev_max_backlog = 5000
net.ipv4.tcp_max_syn_backlog = 65535
net.ipv4.tcp_fin_timeout = 30
net.ipv4.tcp_keepalive_time = 1200
net.ipv4.tcp_max_tw_buckets = 5000
```

#### 文件描述符限制

```bash
# /etc/security/limits.conf
gateway soft nofile 65535
gateway hard nofile 65535
```

### 2. 应用调优

#### 连接池配置

```yaml
server:
  max_connections: 10000
  connection_timeout: "30s"
  idle_timeout: "60s"

middleware:
  rate_limit:
    requests_per_second: 1000
    burst: 2000
```

## 备份和恢复

### 1. 配置备份

```bash
#!/bin/bash
# backup-config.sh

BACKUP_DIR="/backup/gateway-go"
DATE=$(date +%Y%m%d_%H%M%S)

mkdir -p $BACKUP_DIR

# 备份配置文件
cp /etc/gateway-go/config.yaml $BACKUP_DIR/config_$DATE.yaml

# 备份日志
tar -czf $BACKUP_DIR/logs_$DATE.tar.gz /var/log/gateway-go/

# 保留最近30天的备份
find $BACKUP_DIR -name "*.yaml" -mtime +30 -delete
find $BACKUP_DIR -name "*.tar.gz" -mtime +30 -delete
```

### 2. 数据恢复

```bash
#!/bin/bash
# restore-config.sh

BACKUP_FILE=$1
RESTORE_DIR="/tmp/restore"

if [ -z "$BACKUP_FILE" ]; then
    echo "Usage: $0 <backup_file>"
    exit 1
fi

mkdir -p $RESTORE_DIR
tar -xzf $BACKUP_FILE -C $RESTORE_DIR

# 恢复配置
cp $RESTORE_DIR/config.yaml /etc/gateway-go/

# 重启服务
systemctl restart gateway-go
```

## 故障排除

### 1. 常见问题

#### 服务无法启动

```bash
# 检查日志
journalctl -u gateway-go -f

# 检查端口占用
netstat -tlnp | grep 8080

# 检查配置文件
gateway-go -config /etc/gateway-go/config.yaml --validate
```

#### 性能问题

```bash
# 检查系统资源
top
htop
iostat

# 检查网络连接
netstat -i
ss -tuln

# 检查应用状态
curl http://localhost:8080/health
```

### 2. 监控告警

#### 健康检查脚本

```bash
#!/bin/bash
# health-check.sh

HEALTH_URL="http://localhost:8080/health"
MAX_RETRIES=3
RETRY_INTERVAL=5

for i in $(seq 1 $MAX_RETRIES); do
    if curl -f -s $HEALTH_URL > /dev/null; then
        echo "Gateway-Go is healthy"
        exit 0
    fi
    
    echo "Health check failed, attempt $i/$MAX_RETRIES"
    sleep $RETRY_INTERVAL
done

echo "Gateway-Go is unhealthy"
exit 1
```

#### 告警配置

```yaml
# alertmanager.yml
global:
  smtp_smarthost: 'localhost:587'
  smtp_from: 'alertmanager@example.com'

route:
  group_by: ['alertname']
  group_wait: 10s
  group_interval: 10s
  repeat_interval: 1h
  receiver: 'team-mail'

receivers:
- name: 'team-mail'
  email_configs:
  - to: 'team@example.com'
``` 