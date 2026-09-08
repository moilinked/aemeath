// Package auth 提供用户认证与 JWT Access Token 管理。
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

var (
	// ErrInvalidCredentials 表示用户名或密码不正确。
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrInvalidToken 表示 Access Token 缺失、无效或已过期。
	ErrInvalidToken = errors.New("invalid access token")
)

// Config 定义 UserStore 与 JWT 参数。
type Config struct {
	Users      UserStore
	SigningKey []byte
	AccessTTL  time.Duration
	Issuer     string
}

// Identity 是 JWT 验证后得到的用户公开身份，不含密码哈希。
type Identity struct {
	ID        string
	Username  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AccessToken 是成功登录后签发的 Bearer Token。
type AccessToken struct {
	Value     string
	ExpiresAt time.Time
}

// Service 通过 UserStore 校验密码并签发、验证 JWT。
type Service struct {
	users      UserStore
	dummyHash  []byte
	signingKey []byte
	accessTTL  time.Duration
	issuer     string
	now        func() time.Time
}

// New 创建认证服务并校验 JWT 安全参数。
func New(config Config) (*Service, error) {
	if config.Users == nil {
		return nil, errors.New("auth user store is required")
	}
	if strings.TrimSpace(string(config.SigningKey)) == "" {
		return nil, errors.New("JWT signing key is required")
	}
	if config.AccessTTL <= 0 {
		return nil, errors.New("JWT access TTL must be greater than zero")
	}
	issuer := strings.TrimSpace(config.Issuer)
	if issuer == "" {
		return nil, errors.New("JWT issuer is required")
	}

	dummyHash, err := bcrypt.GenerateFromPassword(
		[]byte("invalid-dummy-password"),
		bcrypt.MinCost,
	)
	if err != nil {
		return nil, fmt.Errorf("generate dummy password hash: %w", err)
	}

	return &Service{
		users:      config.Users,
		dummyHash:  dummyHash,
		signingKey: append([]byte(nil), config.SigningKey...),
		accessTTL:  config.AccessTTL,
		issuer:     issuer,
		now:        time.Now,
	}, nil
}

// Authenticate 按用户名查询密码哈希并签发 Access Token。
func (service *Service) Authenticate(
	ctx context.Context,
	username string,
	password string,
) (*AccessToken, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	user, lookupErr := service.users.FindByUsername(ctx, strings.TrimSpace(username))
	if lookupErr != nil && !errors.Is(lookupErr, ErrUserNotFound) {
		return nil, fmt.Errorf("lookup user: %w", lookupErr)
	}

	passwordHash := service.dummyHash
	if lookupErr == nil {
		passwordHash = user.PasswordHash
	}
	passwordErr := bcrypt.CompareHashAndPassword(passwordHash, []byte(password))
	if lookupErr != nil || passwordErr != nil {
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
		Subject:   user.Username,
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
