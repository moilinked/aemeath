package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/ecol/chat-agent/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ auth.UserStore = (*UserStore)(nil)

// UserStore 使用 PostgreSQL 读取用户凭据。
type UserStore struct {
	pool *pgxpool.Pool
}

// NewUserStore 创建 PostgreSQL 用户存储。
func NewUserStore(pool *pgxpool.Pool) *UserStore {
	return &UserStore{pool: pool}
}

// FindByUsername 按用户名查询用户；不存在时返回 auth.ErrUserNotFound。
func (store *UserStore) FindByUsername(
	ctx context.Context,
	username string,
) (auth.User, error) {
	if err := ctx.Err(); err != nil {
		return auth.User{}, err
	}
	if store.pool == nil {
		return auth.User{}, errors.New("postgres pool is required")
	}

	var user auth.User
	var passwordHash string
	err := store.pool.QueryRow(
		ctx,
		`SELECT id, username, password_hash, created_at, updated_at FROM users WHERE username = $1`,
		username,
	).Scan(&user.ID, &user.Username, &passwordHash, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, auth.ErrUserNotFound
	}
	if err != nil {
		return auth.User{}, fmt.Errorf("query user: %w", err)
	}
	user.PasswordHash = []byte(passwordHash)
	return user, nil
}
