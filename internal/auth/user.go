package auth

import (
	"context"
	"errors"
	"strings"
)

// ErrUserNotFound 表示用户名在存储中不存在。
var ErrUserNotFound = errors.New("user not found")

// User 是认证所需的最小用户记录。
type User struct {
	Username     string
	PasswordHash []byte
}

// UserStore 按用户名查询凭据。未知用户必须返回 ErrUserNotFound。
type UserStore interface {
	FindByUsername(ctx context.Context, username string) (User, error)
}

// StaticUserStore 是测试用的固定单用户存储。
type StaticUserStore struct {
	User User
}

// FindByUsername 返回固定用户，或在用户名不匹配时返回 ErrUserNotFound。
func (store StaticUserStore) FindByUsername(
	ctx context.Context,
	username string,
) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	if strings.TrimSpace(store.User.Username) == "" || username != store.User.Username {
		return User{}, ErrUserNotFound
	}
	return User{
		Username:     store.User.Username,
		PasswordHash: append([]byte(nil), store.User.PasswordHash...),
	}, nil
}
