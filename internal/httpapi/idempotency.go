package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
	"unicode"
)

const (
	idempotencyHeader         = "Idempotency-Key"
	maxIdempotencyKeyLength   = 128
	defaultChatIdempotencyTTL = 24 * time.Hour
)

var (
	errIdempotencyInProgress      = errors.New("idempotency key is in progress")
	errIdempotencyPayloadMismatch = errors.New("idempotency key payload mismatch")
)

type idempotencyStore struct {
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]*idempotencyEntry
}

type idempotencyEntry struct {
	payloadHash string
	completed   bool
	statusCode  int
	body        []byte
	expiresAt   time.Time
}

type idempotencyRecord struct {
	Cached     bool
	StatusCode int
	Body       []byte
}

func newIdempotencyStore(ttl time.Duration) *idempotencyStore {
	if ttl <= 0 {
		ttl = defaultChatIdempotencyTTL
	}
	return &idempotencyStore{
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[string]*idempotencyEntry),
	}
}

func (store *idempotencyStore) Begin(key, payloadHash string) (idempotencyRecord, error) {
	now := store.now()

	store.mu.Lock()
	defer store.mu.Unlock()

	entry := store.entries[key]
	if entry != nil && entry.completed && !entry.expiresAt.After(now) {
		delete(store.entries, key)
		entry = nil
	}
	if entry == nil {
		store.entries[key] = &idempotencyEntry{payloadHash: payloadHash}
		return idempotencyRecord{}, nil
	}
	if entry.payloadHash != payloadHash {
		return idempotencyRecord{}, errIdempotencyPayloadMismatch
	}
	if !entry.completed {
		return idempotencyRecord{}, errIdempotencyInProgress
	}
	return idempotencyRecord{
		Cached:     true,
		StatusCode: entry.statusCode,
		Body:       append([]byte(nil), entry.body...),
	}, nil
}

func (store *idempotencyStore) Complete(key string, statusCode int, body []byte) {
	store.mu.Lock()
	defer store.mu.Unlock()

	entry := store.entries[key]
	if entry == nil || entry.completed {
		return
	}
	entry.completed = true
	entry.statusCode = statusCode
	entry.body = append([]byte(nil), body...)
	entry.expiresAt = store.now().Add(store.ttl)
}

func (store *idempotencyStore) Abort(key string) {
	store.mu.Lock()
	defer store.mu.Unlock()

	entry := store.entries[key]
	if entry == nil || entry.completed {
		return
	}
	delete(store.entries, key)
}

func parseIdempotencyKey(value string) (string, error) {
	if value == "" {
		return "", errors.New("Idempotency-Key is required")
	}
	if len(value) > maxIdempotencyKeyLength {
		return "", fmt.Errorf("Idempotency-Key exceeds %d bytes", maxIdempotencyKeyLength)
	}
	for _, character := range value {
		if character > unicode.MaxASCII ||
			!(unicode.IsLetter(character) ||
				unicode.IsDigit(character) ||
				character == '_' ||
				character == '-' ||
				character == '.') {
			return "", errors.New("Idempotency-Key contains invalid characters")
		}
	}
	return value, nil
}

func scopedIdempotencyKey(username, key string) string {
	return username + "\x1f" + key
}

func chatPayloadHash(sessionID, message string) string {
	sum := sha256.Sum256([]byte(sessionID + "\n" + message))
	return hex.EncodeToString(sum[:])
}
