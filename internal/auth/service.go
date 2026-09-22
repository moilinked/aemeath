// Package auth 校验 site 签发的 JWT Access Token。
package auth

import (
	"errors"
	"strings"
	"time"
)

// ErrInvalidToken 表示 Access Token 缺失、无效或已过期。
var ErrInvalidToken = errors.New("invalid access token")

// Config 定义 JWT 校验参数。
type Config struct {
	SigningKey []byte
	Issuer     string
}

// Identity 是 JWT 验证后得到的用户公开身份。
type Identity struct {
	ID           string
	Username     string
	Capabilities Capabilities
}

// CanChat 表示当前身份是否允许调用对话接口。
func (identity Identity) CanChat() bool {
	return identity.Capabilities.CanChat()
}

// Service 校验 site 签发的 JWT，不签发 token、不保存用户。
type Service struct {
	signingKey []byte
	issuer     string
	now        func() time.Time
}

// New 创建认证服务并校验 JWT 安全参数。
func New(config Config) (*Service, error) {
	if strings.TrimSpace(string(config.SigningKey)) == "" {
		return nil, errors.New("JWT signing key is required")
	}
	issuer := strings.TrimSpace(config.Issuer)
	if issuer == "" {
		return nil, errors.New("JWT issuer is required")
	}

	return &Service{
		signingKey: append([]byte(nil), config.SigningKey...),
		issuer:     issuer,
		now:        time.Now,
	}, nil
}
