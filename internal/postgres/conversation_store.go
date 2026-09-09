package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/conversation"
	"github.com/ecol/chat-agent/internal/llm"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ agent.ConversationStore = (*ConversationStore)(nil)

// ConversationStore 使用 PostgreSQL 持久化用户对话与 JSONB 消息。
type ConversationStore struct {
	pool *pgxpool.Pool
}

// NewConversationStore 创建 PostgreSQL 对话存储。
func NewConversationStore(pool *pgxpool.Pool) *ConversationStore {
	return &ConversationStore{pool: pool}
}

// Load 返回指定对话当前消息历史的独立快照。
func (store *ConversationStore) Load(
	ctx context.Context,
	conversationID string,
) ([]llm.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store.pool == nil {
		return nil, errors.New("postgres pool is required")
	}

	var raw []byte
	err := store.pool.QueryRow(
		ctx,
		`SELECT messages FROM conversations WHERE id = $1`,
		conversationID,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return []llm.Message{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load conversation: %w", err)
	}
	return decodeMessages(raw)
}

// Append 按参数顺序将消息原子追加到已存在的对话。
func (store *ConversationStore) Append(
	ctx context.Context,
	conversationID string,
	messages ...llm.Message,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store.pool == nil {
		return errors.New("postgres pool is required")
	}
	if len(messages) == 0 {
		return nil
	}

	payload, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("encode conversation messages: %w", err)
	}

	tag, err := store.pool.Exec(
		ctx,
		`
		UPDATE conversations
		SET messages = messages || $2::jsonb,
		    updated_at = NOW()
		WHERE id = $1
		`,
		conversationID,
		payload,
	)
	if err != nil {
		return fmt.Errorf("append conversation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return agent.ErrConversationNotFound
	}
	return nil
}

// Create 创建属于指定用户的新对话。
func (store *ConversationStore) Create(
	ctx context.Context,
	userID string,
	title string,
) (agent.Conversation, error) {
	if err := ctx.Err(); err != nil {
		return agent.Conversation{}, err
	}
	if store.pool == nil {
		return agent.Conversation{}, errors.New("postgres pool is required")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return agent.Conversation{}, errors.New("conversation user id is required")
	}

	id, err := conversation.NewID()
	if err != nil {
		return agent.Conversation{}, err
	}

	var createdAt, updatedAt time.Time
	err = store.pool.QueryRow(
		ctx,
		`
		INSERT INTO conversations (id, user_id, title, messages)
		VALUES ($1, $2, $3, '[]'::jsonb)
		RETURNING created_at, updated_at
		`,
		id,
		userID,
		strings.TrimSpace(title),
	).Scan(&createdAt, &updatedAt)
	if err != nil {
		return agent.Conversation{}, fmt.Errorf("create conversation: %w", err)
	}
	return agent.Conversation{
		ID:        id,
		Title:     strings.TrimSpace(title),
		CreatedAt: createdAt.UTC(),
		UpdatedAt: updatedAt.UTC(),
	}, nil
}

// GetForUser 读取当前用户拥有的对话。
func (store *ConversationStore) GetForUser(
	ctx context.Context,
	userID string,
	conversationID string,
) (agent.Conversation, []llm.Message, error) {
	if err := ctx.Err(); err != nil {
		return agent.Conversation{}, nil, err
	}
	if store.pool == nil {
		return agent.Conversation{}, nil, errors.New("postgres pool is required")
	}
	userID = strings.TrimSpace(userID)
	conversationID = strings.TrimSpace(conversationID)
	if userID == "" {
		return agent.Conversation{}, nil, errors.New("conversation user id is required")
	}
	if conversationID == "" {
		return agent.Conversation{}, nil, agent.ErrConversationNotFound
	}

	var conversation agent.Conversation
	var raw []byte
	err := store.pool.QueryRow(
		ctx,
		`
		SELECT id, title, messages, created_at, updated_at
		FROM conversations
		WHERE id = $1 AND user_id = $2
		`,
		conversationID,
		userID,
	).Scan(
		&conversation.ID,
		&conversation.Title,
		&raw,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return agent.Conversation{}, nil, agent.ErrConversationNotFound
	}
	if err != nil {
		return agent.Conversation{}, nil, fmt.Errorf("get conversation: %w", err)
	}
	conversation.CreatedAt = conversation.CreatedAt.UTC()
	conversation.UpdatedAt = conversation.UpdatedAt.UTC()
	messages, err := decodeMessages(raw)
	if err != nil {
		return agent.Conversation{}, nil, err
	}
	return conversation, messages, nil
}

// ListForUser 按更新时间倒序返回当前用户的对话摘要。
func (store *ConversationStore) ListForUser(
	ctx context.Context,
	userID string,
) ([]agent.Conversation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store.pool == nil {
		return nil, errors.New("postgres pool is required")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, errors.New("conversation user id is required")
	}

	rows, err := store.pool.Query(
		ctx,
		`
		SELECT id, title, created_at, updated_at
		FROM conversations
		WHERE user_id = $1
		ORDER BY updated_at DESC, id DESC
		`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	items := make([]agent.Conversation, 0)
	for rows.Next() {
		var item agent.Conversation
		if err := rows.Scan(&item.ID, &item.Title, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("list conversations: %w", err)
		}
		item.CreatedAt = item.CreatedAt.UTC()
		item.UpdatedAt = item.UpdatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	return items, nil
}

// DeleteForUser 删除当前用户拥有的对话。
func (store *ConversationStore) DeleteForUser(
	ctx context.Context,
	userID string,
	conversationID string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store.pool == nil {
		return errors.New("postgres pool is required")
	}
	userID = strings.TrimSpace(userID)
	conversationID = strings.TrimSpace(conversationID)
	if userID == "" {
		return errors.New("conversation user id is required")
	}
	if conversationID == "" {
		return agent.ErrConversationNotFound
	}

	tag, err := store.pool.Exec(
		ctx,
		`DELETE FROM conversations WHERE id = $1 AND user_id = $2`,
		conversationID,
		userID,
	)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return agent.ErrConversationNotFound
	}
	return nil
}

func decodeMessages(raw []byte) ([]llm.Message, error) {
	if len(raw) == 0 {
		return []llm.Message{}, nil
	}
	var messages []llm.Message
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, fmt.Errorf("decode conversation messages: %w", err)
	}
	if messages == nil {
		return []llm.Message{}, nil
	}
	return messages, nil
}
