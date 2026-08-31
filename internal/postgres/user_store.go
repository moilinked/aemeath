package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/ecol/chat-agent/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var _ auth.UserStore = (*UserStore)(nil)

// UserStore 使用 PostgreSQL 持久化用户凭据。
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
		`SELECT username, password_hash FROM users WHERE username = $1`,
		username,
	).Scan(&user.Username, &passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, auth.ErrUserNotFound
	}
	if err != nil {
		return auth.User{}, fmt.Errorf("query user: %w", err)
	}
	user.PasswordHash = []byte(passwordHash)
	return user, nil
}

// Upsert 将引导用户写入数据库；已存在时更新密码哈希。
func (store *UserStore) Upsert(
	ctx context.Context,
	username string,
	passwordHash string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store.pool == nil {
		return errors.New("postgres pool is required")
	}

	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("auth username is required")
	}
	if _, err := bcrypt.Cost([]byte(passwordHash)); err != nil {
		return errors.New("auth password hash must be a valid bcrypt hash")
	}

	userID, err := randomID()
	if err != nil {
		return fmt.Errorf("generate user id: %w", err)
	}

	_, err = store.pool.Exec(
		ctx,
		`
		INSERT INTO users (id, username, password_hash)
		VALUES ($1, $2, $3)
		ON CONFLICT (username) DO UPDATE
		SET password_hash = EXCLUDED.password_hash,
		    updated_at = NOW()
		`,
		userID,
		username,
		passwordHash,
	)
	if err != nil {
		return fmt.Errorf("upsert user: %w", err)
	}
	return nil
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
