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
- `ConversationStore` 接口、内存实现与 PostgreSQL 持久化
- `Tool` 接口与并发安全、顺序稳定的 Tool Registry
- 支持基础四则运算、括号和科学计数法的 Calculator Tool
- 使用 Open-Meteo、无需 API Key 的 Weather Tool
- 组合 LLM、Conversation 和 Tools，并限制最大执行步数的 Agent
- 支持工具错误回传、Token 汇总和对话持久化的 Agent Loop
- PostgreSQL 用户存储、bcrypt 密码校验与 Bearer JWT 路由保护
- 受登录保护的 `POST /api/chat`、SSE 流式 `POST /api/chat/stream`、Agent 错误映射与聊天幂等
- 对话是服务端资源：省略 `conversation_id` 会新开对话，带上已有 ID 则续聊
- `GET /api/conversations`、`GET /api/conversations/{id}` 与 `PATCH /api/conversations/{id}` 列出、读取、改标题当前用户的对话
- `DELETE /api/conversations/{id}/messages` 清空当前对话消息并保留会话，后续不再把旧历史送进模型上下文
- LLM 与天气请求对 429/5xx 等可恢复错误进行指数重试

当前已提供受 JWT 保护的 Chat API。客户端应保存服务端返回的 `conversation_id`（或刷新后从对话列表恢复），尚未实现 Web UI。

## MVP 目标

第一阶段目标是实现一个不依赖 Agent Framework 的 Chat Agent Runtime，支持：

- 基础问答与 System Prompt
- 基于服务端 `conversation_id` 的多轮上下文
- Tool Calling 与本地工具执行
- 带最大执行步数的 Agent Loop
- JSON 与 SSE 流式 Chat HTTP API

数据库以外的 Redis、RAG、MCP、长期记忆和 Multi-Agent 不属于当前范围。

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
  ├── Conversation Store
  ├── Tool Registry
  ├── System Prompt
  └── Agent Loop
          │
          ├── Final Answer
          └── Tool Call → Tool Result → LLM
```

各模块保持单向依赖：

- `httpapi` 只负责 HTTP 请求、响应和错误映射。
- `agent` 负责编排 Prompt、Conversation、LLM 和 Tools。
- `llm` 隔离具体模型供应商协议。
- `conversation` 负责对话历史；生产使用 PostgreSQL，测试仍可使用内存存储。
- `postgres` 负责连接池、迁移、UserStore 与 ConversationStore 实现。
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
│   ├── auth/
│   ├── config/
│   ├── httpapi/
│   ├── llm/
│   │   ├── client.go
│   │   ├── openai_compatible.go
│   │   └── types.go
│   ├── postgres/
│   ├── conversation/
│   ├── tools/
│   └── server/
├── docker-compose.yml
├── docs/
│   ├── api.md
│   └── deploy-postgres-linux.md
├── .env.example
├── go.mod
└── README.md
```

功能目录只在对应能力开始实现时创建，不预留空目录。

## 快速开始

### 1. 准备环境

- Go 1.25 或兼容版本
- 可远程访问的 PostgreSQL 16（Linux 部署见 [docs/deploy-postgres-linux.md](docs/deploy-postgres-linux.md)）
- OpenAI 兼容网关或 DeepSeek API Key

### 2. 创建本地配置

```powershell
Copy-Item .env.example .env
```

在 `.env` 中填写当前供应商对应的 API Key，并将 `DATABASE_URL` 改为 Linux 服务器上的 PostgreSQL 连接串（不要使用 `127.0.0.1`）。`.env` 已被 Git 忽略，禁止将真实密钥写入 `.env.example`。服务启动时会执行迁移。登录凭据与对话均读写远程数据库，不要把登录账号或密码写入文档。

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

完整请求/响应、错误码、幂等和 SSE 解析见 [docs/api.md](docs/api.md)。

公开接口：`GET /healthz`、`POST /api/auth/login`。其余 `/api/*` 需要 `Authorization: Bearer <access_token>`。`POST /api/chat` 与 `POST /api/chat/stream` 还必须带 `Idempotency-Key`。

| 方法     | 路径                               | 说明                                          |
| -------- | ---------------------------------- | --------------------------------------------- |
| `GET`    | `/healthz`                         | 健康检查                                      |
| `POST`   | `/api/auth/login`                  | 登录                                          |
| `GET`    | `/api/auth/me`                     | 当前用户（不含对话 ID）                       |
| `GET`    | `/api/conversations`               | 当前用户对话列表                              |
| `GET`    | `/api/conversations/{id}`          | 对话详情与消息历史                            |
| `PATCH`  | `/api/conversations/{id}`          | 修改对话标题                                  |
| `DELETE` | `/api/conversations/{id}/messages` | 清空消息，保留对话                            |
| `DELETE` | `/api/conversations/{id}`          | 删除对话                                      |
| `POST`   | `/api/chat`                        | 非流式发送；省略 `conversation_id` 会新开对话 |
| `POST`   | `/api/chat/stream`                 | SSE 流式发送，请求体与 `/api/chat` 相同       |

## 环境变量

系统环境变量优先级高于 `.env`。

| 环境变量                     | 默认值                      | 说明                                                                 |
| ---------------------------- | --------------------------- | -------------------------------------------------------------------- |
| `SERVER_PORT`                | `8080`                      | HTTP 监听端口，范围为 `1-65535`                                      |
| `SERVER_ADDRESS`             | 无                          | 完整 HTTP 监听地址；设置后优先于 `SERVER_PORT`                       |
| `SERVER_READ_HEADER_TIMEOUT` | `5s`                        | 请求头读取超时                                                       |
| `SERVER_READ_TIMEOUT`        | `15s`                       | 请求读取超时                                                         |
| `SERVER_WRITE_TIMEOUT`       | `30s`                       | 响应写入超时                                                         |
| `SERVER_IDLE_TIMEOUT`        | `60s`                       | 空闲连接超时                                                         |
| `SERVER_SHUTDOWN_TIMEOUT`    | `10s`                       | 优雅关闭超时                                                         |
| `LLM_PROVIDER`               | `deepseek`                  | `openai` 或 `deepseek`                                               |
| `LLM_REQUEST_TIMEOUT`        | `60s`                       | 单次 LLM 请求超时                                                    |
| `LLM_RETRY_MAX_ATTEMPTS`     | `3`                         | LLM 可恢复错误的最大尝试次数，含首次请求                             |
| `LLM_RETRY_INITIAL_INTERVAL` | `200ms`                     | LLM 指数重试的初始间隔                                               |
| `LLM_RETRY_MAX_INTERVAL`     | `2s`                        | LLM 指数重试的最大间隔                                               |
| `AGENT_MAX_STEPS`            | `8`                         | 单次 Agent 运行允许的最大 LLM 决策次数                               |
| `AGENT_CONTEXT_TOKENS`       | `8192`                      | 单次 LLM 请求的估算 prompt token 上限；超出则丢掉最旧完整轮次        |
| `AGENT_MAX_OUTPUT_TOKENS`    | `2048`                      | 单次回复的最大输出 token（`max_tokens`）                             |
| `DATABASE_URL`               | 无                          | 远程 PostgreSQL 连接串，必填；格式见 `docs/deploy-postgres-linux.md` |
| `JWT_SECRET`                 | 无                          | HS256 签名密钥，必填                                                 |
| `JWT_ACCESS_TTL`             | `168h`                      | Access Token 有效期（7 天）                                          |
| `JWT_ISSUER`                 | `chat-agent`                | JWT issuer                                                           |
| `OPENAI_API_KEY`             | 无                          | OpenAI 或兼容网关密钥                                                |
| `OPENAI_BASE_URL`            | `https://api.openai.com/v1` | OpenAI 兼容基础地址                                                  |
| `OPENAI_MODEL`               | 无                          | 网关提供的模型 ID                                                    |
| `DEEPSEEK_API_KEY`           | 无                          | DeepSeek 密钥                                                        |
| `DEEPSEEK_BASE_URL`          | `https://api.deepseek.com`  | DeepSeek 基础地址                                                    |
| `DEEPSEEK_MODEL`             | `deepseek-v4-pro`           | DeepSeek V4 模型 ID                                                  |

## 开发与验证

```powershell
gofmt -w ./cmd ./internal
go vet ./...
go test -count=1 ./...
go build ./cmd/server
```

PostgreSQL 用户与对话存储测试默认跳过。应对准独立测试库，不要使用生产数据库：

```powershell
$env:TEST_DATABASE_URL = "postgres://chat_agent:chat_agent@127.0.0.1:5432/chat_agent?sslmode=disable"
go test -count=1 ./internal/postgres
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
- [x] 定义 `ConversationStore` 接口
- [x] 实现并发安全的内存 Conversation Store
- [x] 定义 `Tool` 接口与 Tool Registry
- [x] 实现 Calculator Tool
- [x] 实现 Weather Tool
- [x] 实现 Agent 与最大执行步数
- [x] 实现 LLM → Tool → Observation → LLM 的 Agent Loop
- [x] 实现固定用户 JWT 登录与 API 路由保护
- [x] 实现 `POST /api/chat`
- [x] 添加请求校验、错误映射和 Agent 集成测试

### 后续阶段

- [x] 使用数据库与 `UserStore` 替代临时环境变量单用户凭据
- [x] Web Chat UI
- [x] SSE 流式响应
- [x] SSE 支持客户端主动断开并取消本次 Chat，停止后续 LLM 与工具调用
- [x] PostgreSQL Conversation 持久化
- [x] 对话由服务端生成 ID；省略 ID 新开对话，带上已有 ID 续聊
- [x] 上下文裁剪和 Token 预算
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
