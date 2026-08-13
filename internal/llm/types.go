// Package llm 定义与具体模型厂商无关的对话接口和数据结构。
package llm

import (
	"context"
	"encoding/json"
)

const (
	// OpenAIBaseURL 是 OpenAI Chat Completions API 的默认基础地址。
	OpenAIBaseURL = "https://api.openai.com/v1"
	// DeepSeekBaseURL 是 DeepSeek OpenAI 兼容 API 的默认基础地址。
	DeepSeekBaseURL = "https://api.deepseek.com"

	// DeepSeekV4Flash 指向 DeepSeek V4 Flash 的滚动版本。
	DeepSeekV4Flash = "deepseek-v4-flash"
	// DeepSeekV4Pro 指向 DeepSeek V4 Pro 的滚动版本。
	DeepSeekV4Pro = "deepseek-v4-pro"
)

// Client 是 Agent Runtime 依赖的最小 LLM 能力。
// 上层只依赖此接口，不感知 OpenAI、DeepSeek 或其他兼容服务。
type Client interface {
	Chat(ctx context.Context, request ChatRequest) (*ChatResponse, error)
}

// Role 表示消息发送方。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 是供应商无关的统一消息结构。
// ReasoningContent 用于在 DeepSeek 思考模式的工具调用循环中回传推理内容。
type Message struct {
	Role             Role       `json:"role"`
	Content          string     `json:"content,omitempty"`
	Name             string     `json:"name,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
}

// FunctionDefinition 描述模型可以调用的函数。
type FunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolDefinition 描述一个可供模型调用的工具。
type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

// FunctionCall 是模型返回的函数调用参数。
// Arguments 是 JSON 字符串，执行工具前应由工具层负责校验和解析。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolCall 表示一次模型工具调用。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// Thinking 配置 DeepSeek V4 的思考模式。
// OpenAI 请求中不应设置该字段，避免兼容性错误。
type Thinking struct {
	Type string `json:"type"`
}

// ChatRequest 是一次非流式对话请求。
type ChatRequest struct {
	Messages        []Message        `json:"messages"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	ToolChoice      json.RawMessage  `json:"tool_choice,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	MaxTokens       *int             `json:"max_tokens,omitempty"`
	Thinking        *Thinking        `json:"thinking,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
}

// ChatResponse 是标准化后的模型响应。
type ChatResponse struct {
	ID           string
	Model        string
	Message      Message
	FinishReason string
	Usage        Usage
}

// Usage 记录模型请求的 token 用量。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
