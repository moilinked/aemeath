package config

import (
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		address     string
		readTimeout string
		wantAddress string
		wantTimeout time.Duration
		wantErr     bool
	}{
		{
			name:        "uses defaults",
			wantAddress: defaultAddress,
			wantTimeout: defaultReadTimeout,
		},
		{
			name:        "reads environment",
			address:     "127.0.0.1:9090",
			readTimeout: "20s",
			wantAddress: "127.0.0.1:9090",
			wantTimeout: 20 * time.Second,
		},
		{
			name:        "rejects invalid duration",
			readTimeout: "invalid",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SERVER_ADDRESS", tt.address)
			t.Setenv("SERVER_READ_TIMEOUT", tt.readTimeout)

			cfg, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Load() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Address != tt.wantAddress {
				t.Errorf("Address = %q, want %q", cfg.Address, tt.wantAddress)
			}
			if cfg.ReadTimeout != tt.wantTimeout {
				t.Errorf("ReadTimeout = %s, want %s", cfg.ReadTimeout, tt.wantTimeout)
			}
		})
	}
}
