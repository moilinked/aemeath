// Package tools 定义 Agent 可调用的本地工具契约与注册表。
package tools

import (
	"context"
	"encoding/json"

	"github.com/ecol/chat-agent/internal/llm"
)

// Tool 是 Agent 可发现并执行的最小工具契约。
//
// Definition 描述暴露给 LLM 的函数及其 JSON Schema。
// Execute 负责校验具体业务参数并返回可作为工具消息内容的文本结果。
type Tool interface {
	Definition() llm.ToolDefinition
	Execute(ctx context.Context, arguments json.RawMessage) (string, error)
}
