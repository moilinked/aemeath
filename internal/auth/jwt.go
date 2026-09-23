package auth

import (
	"context"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Verify 校验 JWT 签名、算法、issuer 与时间声明。
// 用户表在 site 库，chat-agent 只用公钥本地验签；权限变更在 token 过期后生效。
func (service *Service) Verify(
	ctx context.Context,
	tokenValue string,
) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	if strings.TrimSpace(tokenValue) == "" {
		return Identity{}, ErrInvalidToken
	}

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(
		tokenValue,
		claims,
		func(token *jwt.Token) (any, error) {
			return service.publicKey, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(service.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(func() time.Time {
			return service.now().UTC()
		}),
	)
	if err != nil || !token.Valid {
		return Identity{}, ErrInvalidToken
	}
	if claims.Subject == "" ||
		claims.IssuedAt == nil ||
		claims.ExpiresAt == nil {
		return Identity{}, ErrInvalidToken
	}

	username := strings.TrimSpace(claims.Username)
	if username == "" {
		username = claims.Subject
	}
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	return Identity{
		ID:           claims.Subject,
		Username:     username,
		Capabilities: claims.Capabilities,
	}, nil
}
