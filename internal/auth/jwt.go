package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Verify 校验 JWT 签名、算法、issuer、时间声明，并确认用户仍存在。
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

	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(
		tokenValue,
		claims,
		func(token *jwt.Token) (any, error) {
			return service.signingKey, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
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
		claims.ExpiresAt == nil ||
		claims.ID == "" {
		return Identity{}, ErrInvalidToken
	}

	user, err := service.users.FindByUsername(ctx, claims.Subject)
	if errors.Is(err, ErrUserNotFound) {
		return Identity{}, ErrInvalidToken
	}
	if err != nil {
		return Identity{}, fmt.Errorf("lookup user: %w", err)
	}
	if user.Username != claims.Subject {
		return Identity{}, ErrInvalidToken
	}
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	return Identity{
		ID:        user.ID,
		Username:  user.Username,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}, nil
}
