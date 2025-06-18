# 开发环境搭建

## 概述

本文档详细介绍如何搭建 Gateway-Go 的开发环境，包括系统要求、工具安装、项目配置等。

## 系统要求

### 最低要求
- **操作系统**: Linux, macOS, Windows
- **Go 版本**: 1.16 或更高版本
- **内存**: 至少 2GB 可用内存
- **磁盘空间**: 至少 1GB 可用空间

### 推荐配置
- **操作系统**: Linux (Ubuntu 20.04+) 或 macOS
- **Go 版本**: 1.22 或更高版本
- **内存**: 4GB 或更多
- **磁盘空间**: 5GB 或更多
- **CPU**: 2 核心或更多

## 工具安装

### 1. Go 语言环境

#### Linux (Ubuntu/Debian)

```bash
# 下载 Go
wget https://golang.org/dl/go1.22.0.linux-amd64.tar.gz

# 解压到 /usr/local
sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz

# 设置环境变量
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc

# 验证安装
go version
```

#### macOS

```bash
# 使用 Homebrew 安装
brew install go

# 验证安装
go version
```

#### Windows

1. 下载 Go 安装包：https://golang.org/dl/
2. 运行安装程序
3. 设置环境变量
4. 验证安装：`go version`

### 2. Git 版本控制

#### Linux

```bash
sudo apt update
sudo apt install git
```

#### macOS

```bash
brew install git
```

#### Windows

下载并安装：https://git-scm.com/download/win

### 3. IDE 推荐

#### VS Code (推荐)

```bash
# 安装 VS Code
# 下载地址：https://code.visualstudio.com/

# 安装 Go 扩展
code --install-extension golang.go
```

#### GoLand

下载并安装：https://www.jetbrains.com/go/

### 4. 其他工具

#### Make (可选)

```bash
# Linux
sudo apt install make

# macOS
brew install make
```

#### Docker (可选)

```bash
# Linux
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh

# macOS
brew install --cask docker
```

## 项目设置

### 1. 克隆项目

```bash
# 克隆项目
git clone <repository-url>
cd gateway-go

# 检查项目结构
ls -la
```

### 2. 依赖管理

```bash
# 下载依赖
go mod download

# 验证依赖
go mod verify

# 整理依赖
go mod tidy
```

### 3. 环境变量配置

创建 `.env` 文件（可选）：

```bash
# 创建环境变量文件
cat > .env << EOF
GATEWAY_PORT=8080
GATEWAY_MODE=debug
GATEWAY_CONFIG_PATH=config/config.yaml
GATEWAY_LOG_LEVEL=debug
EOF

# 加载环境变量
source .env
```

### 4. 配置文件

确保配置文件存在：

```bash
# 检查配置文件
ls -la config/

# 如果不存在，创建默认配置
cp config/config.yaml.example config/config.yaml
```

## 开发工具配置

### 1. VS Code 配置

创建 `.vscode/settings.json`：

```json
{
    "go.useLanguageServer": true,
    "go.lintTool": "golangci-lint",
    "go.lintOnSave": "package",
    "go.formatTool": "goimports",
    "go.formatOnSave": true,
    "go.testOnSave": false,
    "go.buildOnSave": "package",
    "go.vetOnSave": "package",
    "go.coverOnSave": false,
    "go.gopath": "",
    "go.goroot": "",
    "go.toolsGopath": "",
    "go.gocodeAutoBuild": false,
    "go.gotoSymbol.includeImports": true,
    "go.gotoSymbol.includeGoroot": true,
    "go.gotoSymbol.includeGopath": true,
    "go.gotoSymbol.includeStandardLib": true,
    "go.gotoSymbol.includeVendor": true,
    "go.gotoSymbol.includeTest": true,
    "go.gotoSymbol.includeBenchmark": true,
    "go.gotoSymbol.includeExample": true,
    "go.gotoSymbol.includeFuzz": true,
    "go.gotoSymbol.includeGoroot": true,
    "go.gotoSymbol.includeGopath": true,
    "go.gotoSymbol.includeStandardLib": true,
    "go.gotoSymbol.includeVendor": true,
    "go.gotoSymbol.includeTest": true,
    "go.gotoSymbol.includeBenchmark": true,
    "go.gotoSymbol.includeExample": true,
    "go.gotoSymbol.includeFuzz": true
}
```

### 2. Go 工具安装

```bash
# 安装常用工具
go install golang.org/x/tools/cmd/goimports@latest
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install github.com/go-delve/delve/cmd/dlv@latest
go install golang.org/x/tools/cmd/godoc@latest
go install github.com/axw/gocov/gocov@latest
go install github.com/AlekSi/gocov-xml@latest
```

### 3. 代码格式化配置

创建 `.golangci.yml`：

```yaml
run:
  timeout: 5m
  modules-download-mode: readonly

linters:
  enable:
    - gofmt
    - goimports
    - govet
    - errcheck
    - staticcheck
    - gosimple
    - ineffassign
    - unused
    - misspell
    - gosec
    - prealloc
    - gocritic
    - revive

linters-settings:
  goimports:
    local-prefixes: gateway-go
  gocritic:
    enabled-tags:
      - diagnostic
      - experimental
      - opinionated
      - performance
      - style

issues:
  exclude-rules:
    - path: _test\.go
      linters:
        - errcheck
        - gosec
```

## 构建和运行

### 1. 本地构建

```bash
# 构建项目
go build -o bin/gateway cmd/gateway/main.go

# 运行项目
./bin/gateway
```

### 2. 开发模式运行

```bash
# 直接运行（开发模式）
go run cmd/gateway/main.go

# 或者使用 air 进行热重载
go install github.com/cosmtrek/air@latest
air
```

### 3. 测试运行

```bash
# 运行所有测试
go test ./...

# 运行特定包的测试
go test ./internal/config

# 运行测试并显示覆盖率
go test -cover ./...

# 生成覆盖率报告
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

## 调试配置

### 1. VS Code 调试配置

创建 `.vscode/launch.json`：

```json
{
    "version": "0.2.0",
    "configurations": [
        {
            "name": "Launch Gateway",
            "type": "go",
            "request": "launch",
            "mode": "auto",
            "program": "${workspaceFolder}/cmd/gateway/main.go",
            "env": {},
            "args": []
        },
        {
            "name": "Debug Test",
            "type": "go",
            "request": "launch",
            "mode": "test",
            "program": "${workspaceFolder}",
            "args": [
                "-test.v"
            ]
        }
    ]
}
```

### 2. Delve 调试器

```bash
# 启动调试
dlv debug cmd/gateway/main.go

# 调试测试
dlv test ./internal/config
```

### 3. 性能分析

```bash
# CPU 性能分析
go test -cpuprofile=cpu.prof -bench=. ./internal/router

# 内存性能分析
go test -memprofile=mem.prof -bench=. ./internal/router

# 查看性能报告
go tool pprof cpu.prof
go tool pprof mem.prof
```

## 代码质量工具

### 1. 代码检查

```bash
# 运行 linter
golangci-lint run

# 运行特定检查
golangci-lint run --disable-all --enable=errcheck
```

### 2. 代码格式化

```bash
# 格式化代码
go fmt ./...

# 整理导入
goimports -w .

# 检查代码格式
go vet ./...
```

### 3. 依赖检查

```bash
# 检查依赖更新
go list -u -m all

# 更新依赖
go get -u ./...
go mod tidy
```

## 开发工作流

### 1. 日常开发流程

```bash
# 1. 拉取最新代码
git pull origin main

# 2. 创建功能分支
git checkout -b feature/your-feature

# 3. 开发功能
# ... 编写代码 ...

# 4. 运行测试
go test ./...

# 5. 代码检查
golangci-lint run

# 6. 提交代码
git add .
git commit -m "feat: add your feature"

# 7. 推送分支
git push origin feature/your-feature
```

### 2. 代码审查清单

- [ ] 代码通过所有测试
- [ ] 代码通过 linter 检查
- [ ] 代码格式化正确
- [ ] 添加了必要的注释
- [ ] 更新了相关文档
- [ ] 提交信息清晰明确

### 3. 发布流程

```bash
# 1. 更新版本号
# 修改 go.mod 中的版本

# 2. 生成变更日志
# 记录重要变更

# 3. 创建发布标签
git tag v1.0.0
git push origin v1.0.0

# 4. 构建发布版本
go build -ldflags="-s -w" -o bin/gateway cmd/gateway/main.go
```

## 常见问题

### Q: Go 版本不兼容？
A: 确保使用 Go 1.16 或更高版本，建议使用最新的稳定版本。

### Q: 依赖下载失败？
A: 设置 GOPROXY 环境变量：
```bash
export GOPROXY=https://goproxy.cn,direct
```

### Q: 编译错误？
A: 检查 Go 版本、依赖版本和代码语法。

### Q: 测试失败？
A: 检查测试环境、依赖和测试数据。

### Q: 性能问题？
A: 使用 pprof 工具分析性能瓶颈。

## 下一步

- 📖 查看 [代码规范](standards.md)
- 🧪 学习 [测试指南](testing.md)
- 🐛 了解 [调试技巧](debugging.md)
- 🚀 参考 [部署指南](../deployment.md) 