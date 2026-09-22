package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testUsername = "test-user"
	testUserID   = "test-user-id"
)

var (
	testSigningKey = []byte("0123456789abcdef0123456789abcdef")
	testNow        = time.Date(2026, time.August, 20, 8, 0, 0, 0, time.UTC)
)

func TestNewRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{
			name: "missing signing key",
			config: Config{
				Issuer: "test",
			},
		},
		{
			name: "missing issuer",
			config: Config{
				SigningKey: testSigningKey,
				Issuer:     " ",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.config)
			if err == nil {
				t.Fatal("New() error = nil, want an error")
			}
		})
	}
}

func TestNewAcceptsShortSigningKey(t *testing.T) {
	service, err := New(Config{
		SigningKey: []byte("short"),
		Issuer:     "test",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if service == nil {
		t.Fatal("New() service is nil")
	}
}

func TestVerifySiteToken(t *testing.T) {
	service := newTestService(t)
	token := signedClaimsToken(t, testSigningKey, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "test-issuer",
			Subject:   testUserID,
			ExpiresAt: jwt.NewNumericDate(testNow.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(testNow),
		},
		Username: testUsername,
		Capabilities: Capabilities{
			Chat:        true,
			ManageSite:  true,
			ManageUsers: false,
		},
	})

	identity, err := service.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if identity.ID != testUserID {
		t.Fatalf("Verify() id = %q, want %q", identity.ID, testUserID)
	}
	if identity.Username != testUsername {
		t.Fatalf("Verify() username = %q, want %q", identity.Username, testUsername)
	}
	if !identity.CanChat() {
		t.Fatal("Verify() CanChat() = false, want true")
	}
	if !identity.Capabilities.CanManageSite() {
		t.Fatal("Verify() CanManageSite() = false, want true")
	}
	if identity.Capabilities.CanManageUsers() {
		t.Fatal("Verify() CanManageUsers() = true, want false")
	}
}

func TestVerifyAcceptsTokenWithoutChatCapability(t *testing.T) {
	service := newTestService(t)
	token := signedClaimsToken(t, testSigningKey, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "test-issuer",
			Subject:   testUserID,
			ExpiresAt: jwt.NewNumericDate(testNow.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(testNow),
		},
		Username: testUsername,
	})
	identity, err := service.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if identity.CanChat() {
		t.Fatal("Verify() CanChat() = true, want false")
	}
}

func TestVerifyRejectsInvalidTokens(t *testing.T) {
	validService := newTestService(t)
	validToken := signedClaimsToken(t, testSigningKey, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "test-issuer",
			Subject:   testUserID,
			ExpiresAt: jwt.NewNumericDate(testNow.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(testNow),
			ID:        "token-id",
		},
		Username:     testUsername,
		Capabilities: Capabilities{Chat: true},
	})

	wrongKeyService, err := New(Config{
		SigningKey: []byte("abcdef0123456789abcdef0123456789"),
		Issuer:     "test-issuer",
	})
	if err != nil {
		t.Fatalf("New() wrong key service error = %v", err)
	}
	wrongKeyService.now = func() time.Time { return testNow }

	expiredService := newTestService(t)
	expiredService.now = func() time.Time { return testNow.Add(2 * time.Hour) }

	tests := []struct {
		name    string
		service *Service
		token   string
	}{
		{name: "empty", service: validService, token: ""},
		{name: "malformed", service: validService, token: "not-a-token"},
		{name: "tampered", service: validService, token: validToken + "x"},
		{name: "wrong signature", service: wrongKeyService, token: validToken},
		{name: "expired", service: expiredService, token: validToken},
		{
			name:    "wrong issuer",
			service: validService,
			token: signedToken(t, testSigningKey, jwt.SigningMethodHS256, jwt.RegisteredClaims{
				Issuer:    "wrong-issuer",
				Subject:   testUserID,
				ExpiresAt: jwt.NewNumericDate(testNow.Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(testNow),
			}),
		},
		{
			name:    "wrong algorithm",
			service: validService,
			token: signedToken(
				t,
				jwt.UnsafeAllowNoneSignatureType,
				jwt.SigningMethodNone,
				jwt.RegisteredClaims{
					Issuer:    "test-issuer",
					Subject:   testUserID,
					ExpiresAt: jwt.NewNumericDate(testNow.Add(time.Hour)),
					IssuedAt:  jwt.NewNumericDate(testNow),
				},
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.service.Verify(context.Background(), test.token)
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestServiceHonorsCanceledContext(t *testing.T) {
	service := newTestService(t)
	token := signedClaimsToken(t, testSigningKey, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "test-issuer",
			Subject:   testUserID,
			ExpiresAt: jwt.NewNumericDate(testNow.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(testNow),
		},
		Username: testUsername,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := service.Verify(ctx, token); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify() error = %v, want context.Canceled", err)
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()

	service, err := New(Config{
		SigningKey: testSigningKey,
		Issuer:     "test-issuer",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	service.now = func() time.Time { return testNow }
	return service
}

func signedToken(
	t *testing.T,
	signingKey any,
	method jwt.SigningMethod,
	claims jwt.Claims,
) string {
	t.Helper()

	value, err := jwt.NewWithClaims(method, claims).SignedString(signingKey)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return value
}

func signedClaimsToken(t *testing.T, signingKey any, claims jwt.Claims) string {
	t.Helper()
	return signedToken(t, signingKey, jwt.SigningMethodHS256, claims)
}
