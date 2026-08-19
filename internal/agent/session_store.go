package agent

import (
	"context"

	"github.com/ecol/chat-agent/internal/llm"
)

// SessionStore 持久化不包含 System Prompt 的会话消息历史。
//
// Load 返回按对话顺序排列的独立快照；会话不存在时返回空历史和 nil error。
// Append 必须按参数顺序原子追加消息，并在会话不存在时创建会话。
// Delete 必须是幂等操作。
type SessionStore interface {
	Load(ctx context.Context, sessionID string) ([]llm.Message, error)
	Append(ctx context.Context, sessionID string, messages ...llm.Message) error
	Delete(ctx context.Context, sessionID string) error
}
