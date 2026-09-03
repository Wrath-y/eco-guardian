# Eco Guardian

[English version](README.md)

Eco Guardian 是一个优先面向 Windows 10/11 x64 的本地游戏数值策划分析工具。它让策划在本机维护结构化配置，并在同一套数据上完成校验、版本管理、影响分析、场景模拟和风险复核。

每个项目以 `project.db` 作为业务事实源。运行时只监听本机回环地址（`127.0.0.1`），不会把项目数据或管理接口暴露到局域网。

## 主要能力

- 管理人物、技能、道具、效果、标签、属性及其结构化公式和规则。
- 校验配置引用、公式类型/单位、循环依赖和安全边界。
- 保存不可变配置版本，查看字段级差异，并在发布前执行门禁检查。
- 通过可选的 `local-rag` Graph RAG 服务分析直接/间接依赖，展示影响路径和证据。
- 使用固定场景、版本和随机种子进行可复现模拟，并据此生成数值风险报告。
- 让 AI 生成带证据的结构化草案；草案仍需经过校验、模拟和人工确认，不能直接发布。
- 提供本地项目备份、恢复，以及 Graph/AI 不可用时的能力降级。

## 代码结构

| 路径 | 作用 |
| --- | --- |
| `cmd/eco-guardian` | Go 应用入口 |
| `internal/` | 领域模型、规则校验、HTTP API、版本/模拟/风险/备份和运行时编排 |
| `web/` | React + TypeScript + Ant Design + Vite 前端 |
| `api/openapi.yaml` | HTTP API 契约事实源，同时用于生成前端类型 |
| `migrations/` | SQLite 数据库迁移 |
| `scripts/` | 构建、契约检查和打包辅助脚本 |

生产构建会把 `web/dist`、API 契约、迁移和内置 Schema 嵌入 Go 可执行文件中。`local-rag` 是独立的可选 Graph 服务；AI 功能需要另外配置一个本地或云端 OpenAI-compatible Provider。

## 从源码启动

源码运行支持 Linux、macOS 和 Windows；三者的 External Graph 模式使用同一套
loopback `local-rag` 契约。自带运行时和模型的完整离线安装包目前仍是 Windows
x64 制品。源码构建需要：

- Go 1.25（版本以 `go.mod` 为准）；
- Node.js 22 和 npm；
- Git。

在仓库根目录执行：

```sh
# 安装前端依赖
npm --prefix web ci

# 生成 web/dist；Go 的 //go:embed 需要这些文件
npm --prefix web run build

# 启动本地运行时
go run ./cmd/eco-guardian
```

如果 `local-rag` 仓库与本仓库位于同一父目录，也可以用一个命令依次启动两者：

```sh
# macOS / Linux
./start.sh

# Windows CMD
start.bat
```

脚本会先构建并直接运行 `.run/eco-guardian`，复用已健康的 `local-rag:8765`，否则调用相邻仓库的启动脚本。退出 Eco Guardian 时，脚本只会停止本次由它启动的 local-rag，不会停止原本已在运行的实例。额外参数会原样传给 Eco Guardian；例如 `./start.sh --browser-auto-open false`。如果两个仓库不相邻，可通过 `LOCAL_RAG_DIR` 指定 `local-rag` 仓库路径。

启动后程序会监听一个可用的 `127.0.0.1` 端口，默认自动打开浏览器，并在终端输出访问地址。按 `Ctrl+C` 停止程序。

常用启动参数：

```sh
# 不自动打开浏览器
go run ./cmd/eco-guardian --browser-auto-open false

# 优先使用指定端口；端口被占用时会回退到随机回环端口
go run ./cmd/eco-guardian --preferred-port 31888

# 连接已经运行的本地 local-rag 服务
go run ./cmd/eco-guardian --graph-endpoint http://127.0.0.1:9300
```

运行 `go run ./cmd/eco-guardian --help` 可查看全部启动参数。未配置 `local-rag` 或 AI Provider 时，图检索、影响分析或 AI 相关能力会显示为不可用/降级；本地编辑、校验、版本和备份仍可使用。

## 构建可执行文件

先完成前端构建，再在 Windows 终端执行：

```powershell
npm --prefix web ci
npm --prefix web run build
New-Item -ItemType Directory -Force dist | Out-Null
go build -o dist/eco-guardian.exe ./cmd/eco-guardian
.\dist\eco-guardian.exe
```

如果本地已安装 Make 和仓库使用的 `rtk` 命令，也可以使用 Makefile 快捷目标：

```sh
make build       # 构建前端并编译 eco-guardian
make test        # 运行 Go 测试
make check-contract
```

## 常用检查

```sh
go test ./...
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run lint
npm --prefix web run check:api
```

macOS/Linux 可用于前端开发和大部分测试；当前本地运行时的宿主目录、浏览器和凭据适配器只支持 Windows x64。
