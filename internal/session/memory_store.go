// Package session 提供对话历史的存储实现。
package session

import (
	"context"
	"slices"
	"sync"

	"github.com/ecol/chat-agent/internal/llm"
)

// MemoryStore 在进程内存中并发安全地存储会话消息。
type MemoryStore struct {
	mu        sync.RWMutex
	histories map[string][]llm.Message
}

// NewMemoryStore 创建空的内存会话存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		histories: make(map[string][]llm.Message),
	}
}

// Load 返回指定会话当前消息历史的独立快照。
func (store *MemoryStore) Load(ctx context.Context, sessionID string) ([]llm.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	store.mu.RLock()
	defer store.mu.RUnlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return cloneMessages(store.histories[sessionID]), nil
}

// Append 按参数顺序将消息原子追加到指定会话。
func (store *MemoryStore) Append(ctx context.Context, sessionID string, messages ...llm.Message) error {
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
	if store.histories == nil {
		store.histories = make(map[string][]llm.Message)
	}
	store.histories[sessionID] = append(store.histories[sessionID], cloneMessages(messages)...)
	return nil
}

// Delete 删除指定会话；会话不存在时也返回成功。
func (store *MemoryStore) Delete(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	delete(store.histories, sessionID)
	return nil
}

func cloneMessages(messages []llm.Message) []llm.Message {
	cloned := slices.Clone(messages)
	for index := range cloned {
		cloned[index].ToolCalls = slices.Clone(messages[index].ToolCalls)
	}
	return cloned
}
