package agent

import (
	"errors"
	"sync"

	"github.com/ecol/chat-agent/internal/llm"
	"github.com/ecol/chat-agent/internal/tools"
)

// Config 定义创建 Agent 所需的依赖和执行边界。
//
// MaxSteps 表示单次运行允许的最大 LLM 决策次数，必须大于零。
type Config struct {
	LLM           llm.Client
	Conversations ConversationStore
	Tools         *tools.Registry
	MaxSteps      int
}

// Agent 编排 System Prompt、对话、LLM 和工具。
type Agent struct {
	llmClient           llm.Client
	conversations       ConversationStore
	toolRegistry        *tools.Registry
	systemMessage       llm.Message
	maxSteps            int
	conversationGatesMu sync.Mutex
	conversationGates   map[string]*conversationGate
}

// New 创建 Agent，并校验所有运行依赖和执行边界。
func New(config Config) (*Agent, error) {
	if config.LLM == nil {
		return nil, errors.New("agent LLM client is required")
	}
	if config.Conversations == nil {
		return nil, errors.New("agent conversation store is required")
	}
	if config.Tools == nil {
		return nil, errors.New("agent tool registry is required")
	}
	if config.MaxSteps <= 0 {
		return nil, errors.New("agent max steps must be greater than zero")
	}

	return &Agent{
		llmClient:         config.LLM,
		conversations:     config.Conversations,
		toolRegistry:      config.Tools,
		systemMessage:     SystemMessage(),
		maxSteps:          config.MaxSteps,
		conversationGates: make(map[string]*conversationGate),
	}, nil
}

// MaxSteps 返回单次 Agent 运行允许的最大 LLM 决策次数。
func (agent *Agent) MaxSteps() int {
	return agent.maxSteps
}
