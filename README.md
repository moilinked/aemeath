# Chat Agent

基于 Go 与 go-chi 的 Chat Agent HTTP 服务基础框架。

当前阶段只包含：

- HTTP 服务启动与优雅关闭
- 环境变量配置
- go-chi 路由与通用中间件
- `GET /healthz` 健康检查
- OpenAI Chat Completions 兼容 LLM Client
- OpenAI 网关与 DeepSeek V4 配置切换
- 基础单元测试

暂未实现 Chat API、Session、Tool Registry 和 Agent Loop。

## 启动

首次运行时创建本地配置：

```powershell
Copy-Item .env.example .env
```

在 `.env` 中填写当前供应商对应的 API Key。系统环境变量优先级高于 `.env`。

```bash
go run ./cmd/server
```

默认监听 `:8080`。可通过环境变量覆盖：

```powershell
$env:SERVER_ADDRESS = "127.0.0.1:9090"
go run ./cmd/server
```

## 验证

```powershell
Invoke-RestMethod http://localhost:8080/healthz
```

预期响应：

```json
{
  "status": "ok"
}
```

## 配置

| 环境变量 | 默认值 |
| --- | --- |
| `SERVER_ADDRESS` | `:8080` |
| `SERVER_READ_HEADER_TIMEOUT` | `5s` |
| `SERVER_READ_TIMEOUT` | `15s` |
| `SERVER_WRITE_TIMEOUT` | `30s` |
| `SERVER_IDLE_TIMEOUT` | `60s` |
| `SERVER_SHUTDOWN_TIMEOUT` | `10s` |
| `LLM_PROVIDER` | `deepseek` |
| `LLM_REQUEST_TIMEOUT` | `60s` |
| `OPENAI_API_KEY` | 无 |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` |
| `OPENAI_MODEL` | 无 |
| `DEEPSEEK_API_KEY` | 无 |
| `DEEPSEEK_BASE_URL` | `https://api.deepseek.com` |
| `DEEPSEEK_MODEL` | `deepseek-v4-pro` |
