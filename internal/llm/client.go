package llm

import "context"

// Client 是 Agent Runtime 依赖的最小 LLM 能力。
// 上层只依赖此接口，不感知 OpenAI、DeepSeek 或其他兼容服务。
type Client interface {
	Chat(ctx context.Context, request ChatRequest) (*ChatResponse, error)
}
