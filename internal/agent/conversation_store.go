package agent

import (
	"context"
	"time"

	"github.com/ecol/chat-agent/internal/llm"
)

// Conversation 是一条属于用户的对话摘要，不含消息正文。
type Conversation struct {
	ID        string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ConversationStore 持久化对话元数据与不含 System Prompt 的消息历史。
//
// Load / Append 按对话 ID 读写消息，供 Agent Loop 使用；调用方必须先确认对话存在。
// Create 由服务端生成 ID，并将对话登记到指定用户。
// GetForUser / ListForUser / UpdateTitleForUser / DeleteForUser 只暴露当前用户拥有的对话；
// 不存在或不属于该用户时返回 ErrConversationNotFound。
type ConversationStore interface {
	Load(ctx context.Context, conversationID string) ([]llm.Message, error)
	Append(ctx context.Context, conversationID string, messages ...llm.Message) error
	Create(ctx context.Context, userID string, title string) (Conversation, error)
	GetForUser(ctx context.Context, userID string, conversationID string) (Conversation, []llm.Message, error)
	ListForUser(ctx context.Context, userID string) ([]Conversation, error)
	UpdateTitleForUser(ctx context.Context, userID string, conversationID string, title string) (Conversation, error)
	DeleteForUser(ctx context.Context, userID string, conversationID string) error
}
