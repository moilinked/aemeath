package llm

import "context"

// StreamDelta 是流式输出中的增量片段。
type StreamDelta struct {
	Content          string
	ReasoningContent string
}

// StreamHandler 接收增量内容。返回错误时应中止流式读取。
type StreamHandler func(StreamDelta) error

// Client 是 Agent Runtime 依赖的最小 LLM 能力。
// 上层只依赖此接口，不感知 OpenAI、DeepSeek 或其他兼容服务。
type Client interface {
	Chat(ctx context.Context, request ChatRequest) (*ChatResponse, error)
	ChatStream(ctx context.Context, request ChatRequest, emit StreamHandler) (*ChatResponse, error)
}

// ChatStreamFromChat 把一次非流式 Chat 结果转成流式回调，供测试替身复用。
func ChatStreamFromChat(
	ctx context.Context,
	chat func(context.Context, ChatRequest) (*ChatResponse, error),
	request ChatRequest,
	emit StreamHandler,
) (*ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, err := chat(ctx, request)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, nil
	}
	if err := EmitMessageDeltas(emit, response.Message); err != nil {
		return nil, err
	}
	return response, nil
}

// EmitMessageDeltas 将完整消息按推理内容和正文各发出一次增量。
func EmitMessageDeltas(emit StreamHandler, message Message) error {
	if emit == nil {
		return nil
	}
	if message.ReasoningContent != "" {
		if err := emit(StreamDelta{ReasoningContent: message.ReasoningContent}); err != nil {
			return err
		}
	}
	if message.Content != "" {
		if err := emit(StreamDelta{Content: message.Content}); err != nil {
			return err
		}
	}
	return nil
}
