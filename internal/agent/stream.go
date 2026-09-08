package agent

// StreamEventType 表示 Agent 流式运行中的事件类别。
type StreamEventType string

const (
	// StreamEventDelta 表示最终回答的增量文本。
	StreamEventDelta StreamEventType = "delta"
	// StreamEventReasoning 表示模型思考内容的增量文本。
	StreamEventReasoning StreamEventType = "reasoning"
	// StreamEventToolCall 表示模型决定调用工具。
	StreamEventToolCall StreamEventType = "tool_call"
	// StreamEventToolResult 表示本地工具执行完成。
	StreamEventToolResult StreamEventType = "tool_result"
)

// StreamToolCall 是流式事件中的工具调用或工具结果。
type StreamToolCall struct {
	ID        string
	Name      string
	Arguments string
	Content   string
}

// StreamEvent 是一次 Agent 流式运行向外回传的事件。
type StreamEvent struct {
	Type     StreamEventType
	Content  string
	ToolCall *StreamToolCall
}

// StreamHandler 接收 Agent 流式事件。返回错误时应中止后续 LLM 与工具调用。
type StreamHandler func(StreamEvent) error
