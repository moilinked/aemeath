package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	testUsername = "test-user"
	testUserID   = "test-user-id"
)

var (
	testSigningKey    = []byte("0123456789abcdef0123456789abcdef")
	testNow           = time.Date(2026, time.August, 20, 8, 0, 0, 0, time.UTC)
	testUserCreatedAt = time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	testUserUpdatedAt = time.Date(2026, time.August, 2, 0, 0, 0, 0, time.UTC)
)

func TestNewRejectsInvalidConfig(t *testing.T) {
	users := staticUsersForTest(t)
	tests := []struct {
		name   string
		config Config
	}{
		{
			name: "missing signing key",
			config: Config{
				Users:     users,
				AccessTTL: time.Hour,
				Issuer:    "test",
			},
		},
		{
			name: "non-positive TTL",
			config: Config{
				Users:      users,
				SigningKey: testSigningKey,
				AccessTTL:  0,
				Issuer:     "test",
			},
		},
		{
			name: "blank issuer",
			config: Config{
				Users:      users,
				SigningKey: testSigningKey,
				AccessTTL:  time.Hour,
				Issuer:     " ",
			},
		},
		{
			name: "missing user store",
			config: Config{
				SigningKey: testSigningKey,
				AccessTTL:  time.Hour,
				Issuer:     "test",
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
		Users:      staticUsersForTest(t),
		SigningKey: []byte("short"),
		AccessTTL:  time.Hour,
		Issuer:     "test",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if service == nil {
		t.Fatal("New() service is nil")
	}
}

func TestAuthenticateAndVerify(t *testing.T) {
	service := newTestService(t)

	token, err := service.Authenticate(
		context.Background(),
		testUsername,
		defaultPasswordForTest(),
	)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if token.Value == "" {
		t.Fatal("Authenticate() token is empty")
	}
	if !token.ExpiresAt.Equal(testNow.Add(time.Hour)) {
		t.Fatalf("ExpiresAt = %s, want %s", token.ExpiresAt, testNow.Add(time.Hour))
	}

	identity, err := service.Verify(context.Background(), token.Value)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if identity.Username != testUsername {
		t.Fatalf("Verify() username = %q, want %q", identity.Username, testUsername)
	}
	if identity.ID != testUserID {
		t.Fatalf("Verify() id = %q, want %q", identity.ID, testUserID)
	}
	if !identity.CreatedAt.Equal(testUserCreatedAt) {
		t.Fatalf("Verify() created_at = %s, want %s", identity.CreatedAt, testUserCreatedAt)
	}
	if !identity.UpdatedAt.Equal(testUserUpdatedAt) {
		t.Fatalf("Verify() updated_at = %s, want %s", identity.UpdatedAt, testUserUpdatedAt)
	}
}

func TestAuthenticateRejectsInvalidCredentials(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
	}{
		{
			name:     "wrong username",
			username: "unknown",
			password: defaultPasswordForTest(),
		},
		{
			name:     "wrong password",
			username: testUsername,
			password: "wrong-password",
		},
	}

	service := newTestService(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.Authenticate(
				context.Background(),
				test.username,
				test.password,
			)
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("Authenticate() error = %v, want ErrInvalidCredentials", err)
			}
			if strings.Contains(err.Error(), test.username) ||
				strings.Contains(err.Error(), test.password) {
				t.Fatal("Authenticate() error exposes supplied credentials")
			}
		})
	}
}

func TestVerifyRejectsInvalidTokens(t *testing.T) {
	validService := newTestService(t)
	validToken, err := validService.Authenticate(
		context.Background(),
		testUsername,
		defaultPasswordForTest(),
	)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	wrongKeyService, err := New(Config{
		Users:      staticUsersForTest(t),
		SigningKey: []byte("abcdef0123456789abcdef0123456789"),
		AccessTTL:  time.Hour,
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
		{name: "tampered", service: validService, token: validToken.Value + "x"},
		{name: "wrong signature", service: wrongKeyService, token: validToken.Value},
		{name: "expired", service: expiredService, token: validToken.Value},
		{
			name:    "wrong issuer",
			service: validService,
			token: signedToken(t, testSigningKey, jwt.SigningMethodHS256, jwt.RegisteredClaims{
				Issuer:    "wrong-issuer",
				Subject:   testUsername,
				ExpiresAt: jwt.NewNumericDate(testNow.Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(testNow),
				ID:        "token-id",
			}),
		},
		{
			name:    "wrong subject",
			service: validService,
			token: signedToken(t, testSigningKey, jwt.SigningMethodHS256, jwt.RegisteredClaims{
				Issuer:    "test-issuer",
				Subject:   "other-user",
				ExpiresAt: jwt.NewNumericDate(testNow.Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(testNow),
				ID:        "token-id",
			}),
		},
		{
			name:    "missing token ID",
			service: validService,
			token: signedToken(t, testSigningKey, jwt.SigningMethodHS256, jwt.RegisteredClaims{
				Issuer:    "test-issuer",
				Subject:   testUsername,
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
					Subject:   testUsername,
					ExpiresAt: jwt.NewNumericDate(testNow.Add(time.Hour)),
					IssuedAt:  jwt.NewNumericDate(testNow),
					ID:        "token-id",
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
	token, err := service.Authenticate(
		context.Background(),
		testUsername,
		defaultPasswordForTest(),
	)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := service.Authenticate(ctx, testUsername, defaultPasswordForTest()); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("Authenticate() error = %v, want context.Canceled", err)
	}
	if _, err := service.Verify(ctx, token.Value); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify() error = %v, want context.Canceled", err)
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()

	service, err := New(Config{
		Users:      staticUsersForTest(t),
		SigningKey: testSigningKey,
		AccessTTL:  time.Hour,
		Issuer:     "test-issuer",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	service.now = func() time.Time { return testNow }
	return service
}

func staticUsersForTest(t *testing.T) UserStore {
	t.Helper()
	return StaticUserStore{
		User: User{
			ID:           testUserID,
			Username:     testUsername,
			PasswordHash: passwordHashForTest(t),
			CreatedAt:    testUserCreatedAt,
			UpdatedAt:    testUserUpdatedAt,
		},
	}
}

func signedToken(
	t *testing.T,
	signingKey any,
	method jwt.SigningMethod,
	claims jwt.RegisteredClaims,
) string {
	t.Helper()

	value, err := jwt.NewWithClaims(method, claims).SignedString(signingKey)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return value
}

func defaultPasswordForTest() string {
	return "test-password"
}

func passwordHashForTest(t *testing.T) []byte {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword(
		[]byte(defaultPasswordForTest()),
		bcrypt.MinCost,
	)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	return hash
}
