package httpapi

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestIdempotencyStoreBeginAndReplay(t *testing.T) {
	store := newIdempotencyStore(time.Hour)

	first, err := store.Begin("key-1", "hash-a")
	if err != nil {
		t.Fatalf("Begin() first error = %v", err)
	}
	if first.Cached {
		t.Fatal("Begin() first cached = true, want false")
	}

	if _, err := store.Begin("key-1", "hash-a"); !errors.Is(err, errIdempotencyInProgress) {
		t.Fatalf("Begin() in progress error = %v, want errIdempotencyInProgress", err)
	}

	store.Complete("key-1", 200, []byte(`{"message":"ok"}`))
	replay, err := store.Begin("key-1", "hash-a")
	if err != nil {
		t.Fatalf("Begin() replay error = %v", err)
	}
	if !replay.Cached || replay.StatusCode != 200 || string(replay.Body) != `{"message":"ok"}` {
		t.Fatalf("Begin() replay = %#v, want cached 200 body", replay)
	}
}

func TestIdempotencyStoreRejectsPayloadMismatch(t *testing.T) {
	store := newIdempotencyStore(time.Hour)
	if _, err := store.Begin("key-1", "hash-a"); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	store.Complete("key-1", 200, []byte(`{"message":"ok"}`))

	_, err := store.Begin("key-1", "hash-b")
	if !errors.Is(err, errIdempotencyPayloadMismatch) {
		t.Fatalf("Begin() error = %v, want errIdempotencyPayloadMismatch", err)
	}
}

func TestIdempotencyStoreAbortAllowsRetry(t *testing.T) {
	store := newIdempotencyStore(time.Hour)
	if _, err := store.Begin("key-1", "hash-a"); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	store.Abort("key-1")

	record, err := store.Begin("key-1", "hash-a")
	if err != nil {
		t.Fatalf("Begin() after abort error = %v", err)
	}
	if record.Cached {
		t.Fatal("Begin() after abort cached = true, want false")
	}
}

func TestIdempotencyStoreExpiresCompletedEntries(t *testing.T) {
	store := newIdempotencyStore(time.Minute)
	now := time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	if _, err := store.Begin("key-1", "hash-a"); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	store.Complete("key-1", 200, []byte(`{"message":"ok"}`))

	store.now = func() time.Time { return now.Add(time.Hour) }
	record, err := store.Begin("key-1", "hash-a")
	if err != nil {
		t.Fatalf("Begin() after expiry error = %v", err)
	}
	if record.Cached {
		t.Fatal("Begin() after expiry cached = true, want false")
	}
}

func TestParseIdempotencyKey(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "uuid", value: "550e8400-e29b-41d4-a716-446655440000"},
		{name: "dotted", value: "msg.1_retry-2"},
		{name: "empty", wantErr: true},
		{name: "space", value: "bad key", wantErr: true},
		{name: "slash", value: "a/b", wantErr: true},
		{name: "too long", value: strings.Repeat("a", maxIdempotencyKeyLength+1), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseIdempotencyKey(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatal("parseIdempotencyKey() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseIdempotencyKey() error = %v", err)
			}
			if got != test.value {
				t.Fatalf("parseIdempotencyKey() = %q, want %q", got, test.value)
			}
		})
	}
}
