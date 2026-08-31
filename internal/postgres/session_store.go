package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ecol/chat-agent/internal/agent"
	"github.com/ecol/chat-agent/internal/llm"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ agent.SessionStore = (*SessionStore)(nil)

// SessionStore 使用 PostgreSQL JSONB 持久化会话消息。
type SessionStore struct {
	pool *pgxpool.Pool
}

// NewSessionStore 创建 PostgreSQL 会话存储。
func NewSessionStore(pool *pgxpool.Pool) *SessionStore {
	return &SessionStore{pool: pool}
}

// Load 返回指定会话当前消息历史的独立快照。
func (store *SessionStore) Load(
	ctx context.Context,
	sessionID string,
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
		`SELECT messages FROM sessions WHERE id = $1`,
		sessionID,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return []llm.Message{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	return decodeMessages(raw)
}

// Append 按参数顺序将消息原子追加到指定会话。
func (store *SessionStore) Append(
	ctx context.Context,
	sessionID string,
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
		return fmt.Errorf("encode session messages: %w", err)
	}

	_, err = store.pool.Exec(
		ctx,
		`
		INSERT INTO sessions (id, messages)
		VALUES ($1, $2::jsonb)
		ON CONFLICT (id) DO UPDATE
		SET messages = sessions.messages || EXCLUDED.messages,
		    updated_at = NOW()
		`,
		sessionID,
		payload,
	)
	if err != nil {
		return fmt.Errorf("append session: %w", err)
	}
	return nil
}

// Delete 删除指定会话；会话不存在时也返回成功。
func (store *SessionStore) Delete(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store.pool == nil {
		return errors.New("postgres pool is required")
	}

	_, err := store.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, sessionID)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func decodeMessages(raw []byte) ([]llm.Message, error) {
	if len(raw) == 0 {
		return []llm.Message{}, nil
	}
	var messages []llm.Message
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, fmt.Errorf("decode session messages: %w", err)
	}
	if messages == nil {
		return []llm.Message{}, nil
	}
	return messages, nil
}
