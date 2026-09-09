# Chat Agent 接口对接文档

面向 Web / 客户端对接。默认服务地址 `http://localhost:8080`。当前没有 CORS，跨域需同源部署或由反向代理转发。

登录账号只存在于数据库，本文不提供任何可登录凭据。

## 约定

| 项 | 说明 |
| --- | --- |
| JSON | `Content-Type: application/json`；未知字段会 `400`；响应为 UTF-8，末尾带换行 |
| 时间 | RFC3339，UTC |
| 鉴权 | 除健康检查和登录外，均需 `Authorization: Bearer <access_token>` |
| 错误体 | `{"error":"<message>"}`；`401` 另带 `WWW-Authenticate: Bearer realm="api"` |
| 对话 ID | 由服务端生成（32 位 hex）。客户端只回传已收到的 ID，不可自造新 ID 来开对话 |
| 历史 | 以服务端为准。Chat 请求只传本轮用户输入，不要回传整段 messages |

公开接口：`GET /healthz`、`POST /api/auth/login`。其余 `/api/*` 均需登录。

## 推荐前端模型

和 ChatGPT / Claude 一样：**对话是服务端资源**。

1. 用户点「新对话」：本地清空消息，**不要**调 Chat。等用户发出第一条消息。
2. 第一条消息：`POST /api/chat` 或 `/api/chat/stream`，**省略** `conversation_id`。
3. 用响应里的 `conversation_id` 更新路由（建议 `/c/{id}`）和侧边栏。
4. 同一对话后续消息：必须带上该 `conversation_id`。
5. 刷新页面：`GET /api/conversations` 恢复列表；打开某条时 `GET /api/conversations/{id}` 拉历史。
6. **不要**省略 ID 来「继续上次」——省略 ID 永远是新开对话。

```text
登录 → 拉对话列表
         │
         ├─ 点已有对话 → GET /conversations/{id} → 渲染 messages
         │                    └─ 发送时带 conversation_id
         │
         └─ 新对话（本地空页）→ 首次发送不带 ID → 保存返回的 ID
```

## 接口一览

| 方法 | 路径 | 鉴权 | 幂等键 | 说明 |
| --- | --- | --- | --- | --- |
| `GET` | `/healthz` | 否 | 否 | 健康检查 |
| `POST` | `/api/auth/login` | 否 | 否 | 登录，换 Access Token |
| `GET` | `/api/auth/me` | 是 | 否 | 当前用户 |
| `GET` | `/api/conversations` | 是 | 否 | 对话列表（`updated_at` 倒序） |
| `GET` | `/api/conversations/{id}` | 是 | 否 | 对话详情 + 消息 |
| `PATCH` | `/api/conversations/{id}` | 是 | 否 | 修改对话标题 |
| `DELETE` | `/api/conversations/{id}` | 是 | 否 | 删除对话 |
| `POST` | `/api/chat` | 是 | **必填** | 非流式发送 |
| `POST` | `/api/chat/stream` | 是 | **必填** | SSE 流式发送 |

路径里的 `{id}` 与 JSON 里的 `conversation_id` 规则相同：非空、最长 128 字节，仅允许字母、数字、`_`、`-`、`.`。

## 鉴权

### `POST /api/auth/login`

请求体上限 4 KiB。

```http
POST /api/auth/login
Content-Type: application/json
```

```json
{
  "username": "<username>",
  "password": "<password>"
}
```

成功 `200`：

```json
{
  "access_token": "<jwt>",
  "token_type": "Bearer",
  "expires_in": 604800
}
```

`expires_in` 单位为秒，默认约 7 天（以服务端 `JWT_ACCESS_TTL` 为准）。请把 `access_token` 存到内存或安全存储；后续请求：

```http
Authorization: Bearer <access_token>
```

| 状态 | `error` | 何时 |
| --- | --- | --- |
| 400 | `invalid JSON request body` | JSON 非法、多余字段、多个 JSON 值 |
| 401 | `invalid username or password` | 用户名或密码错误（不区分哪种） |
| 415 | `Content-Type must be application/json` | 不是 JSON |
| 413 | `request body is too large` | 超过 4 KiB |
| 500 | `authentication failed` | 服务内部错误 |

登录成功响应带 `Cache-Control: no-store`。

### `GET /api/auth/me`

成功 `200`：

```json
{
  "id": "<user_id>",
  "username": "<username>",
  "created_at": "<rfc3339>",
  "updated_at": "<rfc3339>"
}
```

不含密码、密码哈希，也不含 `conversation_id`。Token 缺失、格式错误或过期时 `401`，`error` 为 `valid Bearer token required`。

## 对话

列表与详情响应带 `Cache-Control: no-store`。

### `GET /api/conversations`

成功 `200`：

```json
{
  "conversations": [
    {
      "id": "<conversation_id>",
      "title": "帮我计算 128 * 39",
      "created_at": "<rfc3339>",
      "updated_at": "<rfc3339>"
    }
  ]
}
```

没有对话时 `conversations` 为 `[]`。新建时标题取自首条用户消息（空白压缩后最长 40 个字符，超出加 `…`）。可用 `PATCH` 改名。暂无分页。

### `GET /api/conversations/{id}`

成功 `200`：

```json
{
  "id": "<conversation_id>",
  "title": "帮我计算 128 * 39",
  "created_at": "<rfc3339>",
  "updated_at": "<rfc3339>",
  "messages": [
    {
      "role": "user",
      "content": "帮我计算 128 * 39"
    },
    {
      "role": "assistant",
      "content": "128 × 39 = 4992"
    }
  ]
}
```

`messages` 不含 System Prompt。可能出现的 `role`：

| `role` | 含义 |
| --- | --- |
| `user` | 用户消息 |
| `assistant` | 模型回复；若当时在调工具，可能带 `tool_calls` |
| `tool` | 工具执行结果，通常带 `tool_call_id` |

其它可选字段：`name`、`tool_call_id`、`tool_calls`、`reasoning_content`。空历史为 `[]`。

未知 ID、已删除或不属于当前用户：**一律 `404`**，`error` 为 `conversation not found`，不要对 403 做分支。

| 状态 | `error` |
| --- | --- |
| 400 | `conversation_id is invalid` |
| 401 | `valid Bearer token required` |
| 404 | `conversation not found` |
| 500 | `conversation request failed` |

### `PATCH /api/conversations/{id}`

请求体上限 4 KiB。不需要 `Idempotency-Key`。

```http
PATCH /api/conversations/{id}
Authorization: Bearer <access_token>
Content-Type: application/json
```

```json
{
  "title": "新标题"
}
```

`title` 会压缩连续空白。去空白后不能为空，也不能超过 40 个字符（按 Unicode 码点计）；超长不会截断。成功会更新 `updated_at`，列表仍按更新时间倒序。

成功 `200`，返回摘要（便于刷新侧边栏）：

```json
{
  "id": "<conversation_id>",
  "title": "新标题",
  "created_at": "<rfc3339>",
  "updated_at": "<rfc3339>"
}
```

未知 ID、已删除或不属于当前用户：**一律 `404`**，`error` 为 `conversation not found`。

| 状态 | `error` | 何时 |
| --- | --- | --- |
| 400 | `conversation_id is invalid` | 路径 ID 非法 |
| 400 | `title is required` | 缺字段、空字符串或全是空白 |
| 400 | `title is too long` | 超过 40 个字符 |
| 400 | `invalid JSON request body` | JSON 非法、多余字段、多个 JSON 值 |
| 401 | `valid Bearer token required` | 未登录或 Token 无效 |
| 404 | `conversation not found` | 未知 / 他人 / 已删除 |
| 415 | `Content-Type must be application/json` | 不是 JSON |
| 413 | `request body is too large` | 超过 4 KiB |
| 500 | `conversation request failed` | 服务内部错误 |

响应带 `Cache-Control: no-store`。

### `DELETE /api/conversations/{id}`

成功 `204`，无响应体。未知 / 他人 ID 同样 `404`。

## Chat

Chat 请求体上限 64 KiB。`message` 去空白后不能为空。

```http
POST /api/chat
Authorization: Bearer <access_token>
Content-Type: application/json
Idempotency-Key: <per-send-key>
```

```json
{
  "conversation_id": "<optional-existing-id>",
  "message": "帮我计算 128 * 39"
}
```

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `message` | 是 | 本轮用户输入 |
| `conversation_id` | 否 | 省略 = **新开对话**；传入已有且属于自己的 ID = 续聊 |

成功 `200`：

```json
{
  "conversation_id": "<id>",
  "message": "128 × 39 = 4992"
}
```

新对话时请立刻保存返回的 `conversation_id`。流式接口的 `done` 事件里也有同一字段。

### 幂等 `Idempotency-Key`

`POST /api/chat` 与 `POST /api/chat/stream` **必须**带。建议每次用户点击发送生成一把新键（UUID 即可）；超时或网络失败后**用同一把键重试**，避免重复跑 Agent、重复扣 Token。

| 规则 | 值 |
| --- | --- |
| 字符 | ASCII 字母、数字、`_`、`-`、`.` |
| 长度 | 1–128 字节 |
| 作用域 | 按登录用户隔离 |
| 指纹 | 请求里的 `conversation_id`（省略则为空）+ `message` |
| 缓存 | 进程内存，默认 24 小时；服务重启后失效 |

| 情况 | 行为 |
| --- | --- |
| 同用户、同键、同内容，且上次已完成 | 直接返回首次结果，不再调用模型 |
| 同键但仍在处理 | `409` `chat request is already in progress` |
| 同键但 `conversation_id` 或 `message` 不同 | `409` `Idempotency-Key already used with a different request` |
| 客户端断开导致取消 | **不**写入缓存，可用同一把键重试 |

流式成功后的重放：HTTP `200` + SSE，通常只有一条 `done`（完整最终回答），没有增量。

Agent 失败且已写入缓存时（例如 `502`），同键重放会再次返回该错误 JSON，不会改走 SSE。

### Chat 状态码

| 状态 | `error` | 说明 |
| --- | --- | --- |
| 400 | `chat context exceeds token budget` | System Prompt、当前轮次或工具定义已超过预算 |
| 400 | `message is required` | 空消息 |
| 400 | `conversation_id is invalid` | ID 字符或长度不合法 |
| 400 | `Idempotency-Key is required` 等 | 缺少或非法幂等键 |
| 401 | `valid Bearer token required` | 未登录或 Token 无效 |
| 404 | `conversation not found` | ID 不存在或不属于当前用户 |
| 408 | `chat request canceled` | 请求被取消（非流式） |
| 409 | 见上表 | 幂等冲突 |
| 413 | `request body is too large` | 超过 64 KiB |
| 415 | `Content-Type must be application/json` | 不是 JSON |
| 500 | `chat completion failed` | 空结果等内部失败 |
| 502 | `chat completion failed` | 上游模型或工具调用失败 |
| 504 | `chat request timed out` / `agent exceeded maximum execution steps` | 超时或步数耗尽 |

## SSE：`POST /api/chat/stream`

请求头、请求体、鉴权、幂等与 `POST /api/chat` 相同。

浏览器原生 `EventSource` 只支持 GET，前端必须用 `fetch`（或等价 HTTP 客户端）读 `ReadableStream`。

校验失败（缺 Token、缺幂等键、非法 JSON、404 等）仍返回 **JSON** 错误，不是 SSE。只有进入流之后才是：

```http
HTTP/1.1 200 OK
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no
```

事件格式：

```text
event: delta
data: {"content":"128"}

event: done
data: {"conversation_id":"<id>","message":"128 × 39 = 4992"}

```

| event | data | 说明 |
| --- | --- | --- |
| `delta` | `{"content":"..."}` | 最终回答增量，按顺序拼接 |
| `reasoning` | `{"content":"..."}` | 思考内容增量（可单独展示，不要并进气泡正文） |
| `tool_call` | `{"id","name","arguments"}` | 模型决定调用工具；`arguments` 是 JSON 字符串 |
| `tool_result` | `{"id","name","content"}` | 本地工具结果 |
| `done` | `{"conversation_id","message"}` | 完整最终回答，流正常结束 |
| `error` | `{"error":"..."}` | 流已开始后的失败；HTTP 状态仍是 200 |

建议解析逻辑：

1. 先看 HTTP 状态。非 200 则按 JSON `error` 处理。
2. 按空行分帧，读 `event:` / `data:`。
3. 将所有 `delta` 拼成当前气泡；收到 `done` 后用 `message` 覆盖为最终文本，并写入 `conversation_id`。
4. 收到 `error` 或连接中断：标记本轮失败。若用户取消（`AbortController`），服务端会停掉后续 LLM 和工具，且不缓存幂等结果，可用同一 `Idempotency-Key` 重试。

典型顺序：`tool_call` → `tool_result`（可多轮）→ 若干 `delta` / `reasoning` → `done`。不是每次都会调工具。

## 客户端注意点

1. **新对话不要带旧 ID**，也不要省略 ID 指望续上最近一次。
2. 侧边栏只信 `GET /api/conversations`，不要用 `/api/auth/me` 找当前对话。
3. 渲染历史用详情接口的 `messages`，不要在本地另存一份当作权威数据。发给模型的上下文可能已按 token 预算裁掉旧轮次，**不会**改写库里的历史。
4. 工具调用轮次会出现 `assistant`（带 `tool_calls`）和 `tool` 消息；UI 可折叠展示，不要当成普通聊天气泡重复渲染。
5. 服务端暂无置顶、分页、分享链接。改标题用 `PATCH /api/conversations/{id}`。
6. 幂等缓存在单进程内存：多副本部署时，重试必须打到同一实例才命中；重启后键失效，重试会再跑一轮。

## 调用示例

登录（把用户名密码换成环境里的真实账号，不要写进仓库）：

```bash
curl -sS http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d "{\"username\":\"<username>\",\"password\":\"<password>\"}"
```

新开对话（流式）：

```bash
curl -N http://localhost:8080/api/chat/stream \
  -H "Authorization: Bearer <access_token>" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: 11111111-1111-1111-1111-111111111111" \
  -d "{\"message\":\"帮我计算 128 * 39\"}"
```

续聊：

```bash
curl -sS http://localhost:8080/api/chat \
  -H "Authorization: Bearer <access_token>" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: 22222222-2222-2222-2222-222222222222" \
  -d "{\"conversation_id\":\"<id>\",\"message\":\"再解释一下\"}"
```

列表与详情：

```bash
curl -sS http://localhost:8080/api/conversations \
  -H "Authorization: Bearer <access_token>"

curl -sS http://localhost:8080/api/conversations/<id> \
  -H "Authorization: Bearer <access_token>"

curl -sS -X PATCH http://localhost:8080/api/conversations/<id> \
  -H "Authorization: Bearer <access_token>" \
  -H "Content-Type: application/json" \
  -d "{\"title\":\"新标题\"}"
```
