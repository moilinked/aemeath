// Package auth 校验 site 签发的 JWT Access Token。
package auth

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"time"
)

// ErrInvalidToken 表示 Access Token 缺失、无效或已过期。
var ErrInvalidToken = errors.New("invalid access token")

// Config 定义 JWT 校验参数。
type Config struct {
	PublicKey ed25519.PublicKey
	Issuer    string
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

// Service 使用 site 的 Ed25519 公钥本地校验 JWT，不签发 token、不保存用户、不回调 site。
type Service struct {
	publicKey ed25519.PublicKey
	issuer    string
	now       func() time.Time
}

// New 创建认证服务并校验 JWT 安全参数。
func New(config Config) (*Service, error) {
	if len(config.PublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("JWT public key is invalid")
	}
	issuer := strings.TrimSpace(config.Issuer)
	if issuer == "" {
		return nil, errors.New("JWT issuer is required")
	}

	return &Service{
		publicKey: append(ed25519.PublicKey(nil), config.PublicKey...),
		issuer:    issuer,
		now:       time.Now,
	}, nil
}
