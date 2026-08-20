// Package auth 提供固定用户认证与 JWT Access Token 管理。
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const minimumSigningKeyLength = 32

var (
	// ErrInvalidCredentials 表示用户名或密码不正确。
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrInvalidToken 表示 Access Token 缺失、无效或已过期。
	ErrInvalidToken = errors.New("invalid access token")
)

// TODO: 接入用户数据库后，将 Username 和 PasswordHash 替换为 UserStore 依赖。
// Config 定义临时单用户凭据与 JWT 参数。
type Config struct {
	Username     string
	PasswordHash []byte
	SigningKey   []byte
	AccessTTL    time.Duration
	Issuer       string
}

// Identity 是 JWT 验证后得到的用户身份。
type Identity struct {
	Username string
}

// AccessToken 是成功登录后签发的 Bearer Token。
type AccessToken struct {
	Value     string
	ExpiresAt time.Time
}

// Service 校验固定用户密码并签发、验证 JWT。
type Service struct {
	username     string
	passwordHash []byte
	signingKey   []byte
	accessTTL    time.Duration
	issuer       string
	now          func() time.Time
}

// New 创建认证服务并校验 JWT 安全参数。
func New(config Config) (*Service, error) {
	username := strings.TrimSpace(config.Username)
	if username == "" {
		return nil, errors.New("auth username is required")
	}
	if _, err := bcrypt.Cost(config.PasswordHash); err != nil {
		return nil, errors.New("auth password hash must be a valid bcrypt hash")
	}
	if len(config.SigningKey) < minimumSigningKeyLength {
		return nil, fmt.Errorf(
			"JWT signing key must be at least %d bytes",
			minimumSigningKeyLength,
		)
	}
	if config.AccessTTL <= 0 {
		return nil, errors.New("JWT access TTL must be greater than zero")
	}
	issuer := strings.TrimSpace(config.Issuer)
	if issuer == "" {
		return nil, errors.New("JWT issuer is required")
	}

	return &Service{
		username:     username,
		passwordHash: append([]byte(nil), config.PasswordHash...),
		signingKey:   append([]byte(nil), config.SigningKey...),
		accessTTL:    config.AccessTTL,
		issuer:       issuer,
		now:          time.Now,
	}, nil
}

// Authenticate 校验固定用户凭据并签发 Access Token。
func (service *Service) Authenticate(
	ctx context.Context,
	username string,
	password string,
) (*AccessToken, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// TODO: 支持多用户时，通过 UserStore 按 username 查询密码哈希；
	// 未知用户仍需使用固定的 dummy hash 执行 bcrypt 比较，避免用户枚举时序差异。
	passwordErr := bcrypt.CompareHashAndPassword(
		service.passwordHash,
		[]byte(password),
	)
	if username != service.username || passwordErr != nil {
		return nil, ErrInvalidCredentials
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	now := service.now().UTC()
	expiresAt := now.Add(service.accessTTL)
	tokenID, err := randomTokenID()
	if err != nil {
		return nil, fmt.Errorf("generate JWT ID: %w", err)
	}

	claims := jwt.RegisteredClaims{
		Issuer:    service.issuer,
		Subject:   service.username,
		ExpiresAt: jwt.NewNumericDate(expiresAt),
		IssuedAt:  jwt.NewNumericDate(now),
		ID:        tokenID,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(service.signingKey)
	if err != nil {
		return nil, fmt.Errorf("sign access token: %w", err)
	}
	return &AccessToken{
		Value:     signed,
		ExpiresAt: expiresAt,
	}, nil
}

func randomTokenID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
