# Chat Agent

一个使用 Go 与 go-chi 自主实现的轻量级 Chat Agent Runtime。

项目不会自研大模型，也暂不依赖 LangGraph 等 Agent Framework。第一阶段聚焦于使用现有 LLM API，逐步实现会话状态、System Prompt、工具调用和 Agent Loop，形成最小但完整的 Chat Agent 闭环。

## 当前状态

当前处于基础设施阶段，已经完成：

- go-chi HTTP 服务、通用中间件和优雅关闭
- `GET /healthz` 健康检查
- `.env` 与系统环境变量配置
- OpenAI Chat Completions 兼容的 LLM Client
- OpenAI 兼容网关与 DeepSeek V4 配置切换
- 工具调用、DeepSeek 思考内容和 Token Usage 数据结构
- 配置与 LLM Client 单元测试
- 版本受控、供应商无关的默认 System Prompt

尚未实现 Chat API、Session、Tool Registry 和 Agent Loop，因此当前服务还不能直接进行聊天。

## MVP 目标

第一阶段目标是实现一个不依赖 Agent Framework 的 Chat Agent Runtime，支持：

- 基础问答与 System Prompt
- 基于 Session ID 的多轮上下文
- Tool Calling 与本地工具执行
- 带最大执行步数的 Agent Loop
- JSON 格式的 Chat HTTP API

数据库、Redis、RAG、MCP、长期记忆和 Multi-Agent 不属于第一阶段范围。

## 目标架构

```text
Client
  │
  ▼
Go HTTP API
  │
  ▼
Chat Handler
  │
  ▼
Agent Runtime
  ├── LLM Client
  ├── Session Store
  ├── Tool Registry
  ├── System Prompt
  └── Agent Loop
          │
          ├── Final Answer
          └── Tool Call → Tool Result → LLM
```

各模块保持单向依赖：

- `httpapi` 只负责 HTTP 请求、响应和错误映射。
- `agent` 负责编排 Prompt、Session、LLM 和 Tools。
- `llm` 隔离具体模型供应商协议。
- `session` 负责对话历史，MVP 阶段使用并发安全的内存存储。
- `tools` 负责工具契约、注册和执行。
- `config` 统一加载环境配置，业务包不直接读取 `.env`。

## 当前项目结构

```text
chat-agent/
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── agent/
│   ├── config/
│   ├── httpapi/
│   ├── llm/
│   │   ├── client.go
│   │   ├── openai_compatible.go
│   │   └── types.go
│   └── server/
├── .env.example
├── go.mod
└── README.md
```

`session` 和 `tools` 会在对应功能开发时创建，不预留空目录。

## 快速开始

### 1. 准备环境

- Go 1.25 或兼容版本
- OpenAI 兼容网关或 DeepSeek API Key

### 2. 创建本地配置

```powershell
Copy-Item .env.example .env
```

在 `.env` 中填写当前供应商对应的 API Key。`.env` 已被 Git 忽略，禁止将真实密钥写入 `.env.example`。

### 3. 选择模型

DeepSeek V4：

```dotenv
LLM_PROVIDER=deepseek
DEEPSEEK_API_KEY=your-api-key
DEEPSEEK_BASE_URL=https://api.deepseek.com
DEEPSEEK_MODEL=deepseek-v4-pro
```

OpenAI 兼容网关：

```dotenv
LLM_PROVIDER=openai
OPENAI_API_KEY=your-api-key
OPENAI_BASE_URL=https://your-compatible-gateway.example.com/v1
OPENAI_MODEL=chat-gpt-luna
```

`chat-gpt-luna` 不是 OpenAI 官方公开模型 ID，应将 `OPENAI_BASE_URL` 配置为支持该模型别名的兼容网关。

### 4. 启动服务

```powershell
go run ./cmd/server
```

默认监听 `:8080`。

### 5. 验证服务

```powershell
Invoke-RestMethod http://localhost:8080/healthz
```

预期响应：

```json
{
  "status": "ok"
}
```

## HTTP API

### 健康检查

```text
GET /healthz
```

### Chat API（待实现）

计划提供：

```text
POST /api/chat
```

计划请求：

```json
{
  "session_id": "user-123",
  "message": "帮我计算 128 * 39"
}
```

计划响应：

```json
{
  "message": "128 × 39 = 4992"
}
```

## 环境变量

系统环境变量优先级高于 `.env`。

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `SERVER_ADDRESS` | `:8080` | HTTP 监听地址 |
| `SERVER_READ_HEADER_TIMEOUT` | `5s` | 请求头读取超时 |
| `SERVER_READ_TIMEOUT` | `15s` | 请求读取超时 |
| `SERVER_WRITE_TIMEOUT` | `30s` | 响应写入超时 |
| `SERVER_IDLE_TIMEOUT` | `60s` | 空闲连接超时 |
| `SERVER_SHUTDOWN_TIMEOUT` | `10s` | 优雅关闭超时 |
| `LLM_PROVIDER` | `deepseek` | `openai` 或 `deepseek` |
| `LLM_REQUEST_TIMEOUT` | `60s` | 单次 LLM 请求超时 |
| `OPENAI_API_KEY` | 无 | OpenAI 或兼容网关密钥 |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | OpenAI 兼容基础地址 |
| `OPENAI_MODEL` | 无 | 网关提供的模型 ID |
| `DEEPSEEK_API_KEY` | 无 | DeepSeek 密钥 |
| `DEEPSEEK_BASE_URL` | `https://api.deepseek.com` | DeepSeek 基础地址 |
| `DEEPSEEK_MODEL` | `deepseek-v4-pro` | DeepSeek V4 模型 ID |

## 开发与验证

```powershell
gofmt -w ./cmd ./internal
go vet ./...
go test -count=1 ./...
go build ./cmd/server
```

真实 DeepSeek 最小连通性测试默认不会随单元测试运行。配置 `.env` 后手动执行：

```powershell
go test -tags=integration -run "^TestDeepSeekConnectivity$" -count=1 ./internal/llm
```

该测试只发送一次关闭思考模式、限制为 8 个输出 Token 的请求，但仍会产生少量 API 费用。

## TODO

### 基础设施

- [x] 初始化 Go 与 go-chi HTTP 项目
- [x] 添加健康检查和优雅关闭
- [x] 添加 `.env`、供应商选择和超时配置
- [x] 定义厂商无关的 `llm.Client`
- [x] 实现 OpenAI Chat Completions 兼容客户端
- [x] 将 LLM 配置与 Client 注入应用启动流程
- [x] 添加真实模型的最小连通性测试

### Chat Agent MVP

- [x] 定义 System Prompt
- [ ] 定义 `SessionStore` 接口
- [ ] 实现并发安全的内存 Session Store
- [ ] 定义 `Tool` 接口与 Tool Registry
- [ ] 实现 Calculator Tool
- [ ] 实现 Weather Tool
- [ ] 实现 Agent 与最大执行步数
- [ ] 实现 LLM → Tool → Observation → LLM 的 Agent Loop
- [ ] 实现 `POST /api/chat`
- [ ] 添加请求校验、错误映射和 Agent 集成测试

### 后续阶段

- [ ] Web Chat UI
- [ ] SSE 流式响应
- [ ] PostgreSQL 或 Redis Session 持久化
- [ ] 上下文裁剪和 Token 预算
- [ ] Tracing 与 Evals
- [ ] RAG 与搜索工具
- [ ] MCP
- [ ] Multi-Agent

## 设计原则

- 先完成最小闭环，再引入状态机、Graph 或复杂框架。
- Agent 只依赖项目内部定义的接口，不直接依赖供应商 SDK 类型。
- 工具、会话和 LLM Provider 可独立替换。
- 所有阻塞操作传递 `context.Context`。
- API Key 和 Token 不进入日志、测试快照或版本控制。
