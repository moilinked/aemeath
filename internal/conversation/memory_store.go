// Package conversation 提供对话历史的存储实现。
package conversation

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/llm"
)

type storedConversation struct {
	userID    string
	title     string
	createdAt time.Time
	updatedAt time.Time
	messages  []llm.Message
}

// MemoryStore 在进程内存中并发安全地存储用户对话。
type MemoryStore struct {
	mu            sync.RWMutex
	conversations map[string]*storedConversation
	now           func() time.Time
}

// NewMemoryStore 创建空的内存对话存储。
func NewMemoryStore() *MemoryStore {
	start := time.Now().UTC()
	var seq atomic.Int64
	return &MemoryStore{
		conversations: make(map[string]*storedConversation),
		now: func() time.Time {
			return start.Add(time.Duration(seq.Add(1)) * time.Millisecond)
		},
	}
}

// Load 返回指定对话当前消息历史的独立快照。
func (store *MemoryStore) Load(ctx context.Context, conversationID string) ([]llm.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	store.mu.RLock()
	defer store.mu.RUnlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	record := store.conversations[conversationID]
	if record == nil {
		return []llm.Message{}, nil
	}
	return cloneMessages(record.messages), nil
}

// Append 按参数顺序将消息原子追加到已存在的对话。
func (store *MemoryStore) Append(ctx context.Context, conversationID string, messages ...llm.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(messages) == 0 {
		return nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	record := store.conversations[conversationID]
	if record == nil {
		return agent.ErrConversationNotFound
	}
	record.messages = append(record.messages, cloneMessages(messages)...)
	record.updatedAt = store.now()
	return nil
}

// Create 创建属于指定用户的新对话。
func (store *MemoryStore) Create(ctx context.Context, userID string, title string) (agent.Conversation, error) {
	if err := ctx.Err(); err != nil {
		return agent.Conversation{}, err
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return agent.Conversation{}, errors.New("conversation user id is required")
	}

	id, err := NewID()
	if err != nil {
		return agent.Conversation{}, err
	}
	now := store.now()
	record := &storedConversation{
		userID:    userID,
		title:     strings.TrimSpace(title),
		createdAt: now,
		updatedAt: now,
		messages:  []llm.Message{},
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return agent.Conversation{}, err
	}
	if store.conversations == nil {
		store.conversations = make(map[string]*storedConversation)
	}
	store.conversations[id] = record
	return record.summary(id), nil
}

// GetForUser 读取当前用户拥有的对话。
func (store *MemoryStore) GetForUser(
	ctx context.Context,
	userID string,
	conversationID string,
) (agent.Conversation, []llm.Message, error) {
	if err := ctx.Err(); err != nil {
		return agent.Conversation{}, nil, err
	}
	userID = strings.TrimSpace(userID)
	conversationID = strings.TrimSpace(conversationID)
	if userID == "" {
		return agent.Conversation{}, nil, errors.New("conversation user id is required")
	}
	if conversationID == "" {
		return agent.Conversation{}, nil, agent.ErrConversationNotFound
	}

	store.mu.RLock()
	defer store.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return agent.Conversation{}, nil, err
	}
	record := store.conversations[conversationID]
	if record == nil || record.userID != userID {
		return agent.Conversation{}, nil, agent.ErrConversationNotFound
	}
	return record.summary(conversationID), cloneMessages(record.messages), nil
}

// ListForUser 按更新时间倒序返回当前用户的对话摘要。
func (store *MemoryStore) ListForUser(ctx context.Context, userID string) ([]agent.Conversation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, errors.New("conversation user id is required")
	}

	store.mu.RLock()
	defer store.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	items := make([]agent.Conversation, 0)
	for id, record := range store.conversations {
		if record.userID != userID {
			continue
		}
		items = append(items, record.summary(id))
	}
	slices.SortFunc(items, func(a, b agent.Conversation) int {
		if a.UpdatedAt.Equal(b.UpdatedAt) {
			return strings.Compare(b.ID, a.ID)
		}
		if a.UpdatedAt.Before(b.UpdatedAt) {
			return 1
		}
		return -1
	})
	return items, nil
}

// DeleteForUser 删除当前用户拥有的对话。
func (store *MemoryStore) DeleteForUser(ctx context.Context, userID string, conversationID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	userID = strings.TrimSpace(userID)
	conversationID = strings.TrimSpace(conversationID)
	if userID == "" {
		return errors.New("conversation user id is required")
	}
	if conversationID == "" {
		return agent.ErrConversationNotFound
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	record := store.conversations[conversationID]
	if record == nil || record.userID != userID {
		return agent.ErrConversationNotFound
	}
	delete(store.conversations, conversationID)
	return nil
}

func (record *storedConversation) summary(id string) agent.Conversation {
	return agent.Conversation{
		ID:        id,
		Title:     record.title,
		CreatedAt: record.createdAt,
		UpdatedAt: record.updatedAt,
	}
}

func cloneMessages(messages []llm.Message) []llm.Message {
	cloned := slices.Clone(messages)
	for index := range cloned {
		cloned[index].ToolCalls = slices.Clone(messages[index].ToolCalls)
	}
	if cloned == nil {
		return []llm.Message{}
	}
	return cloned
}
