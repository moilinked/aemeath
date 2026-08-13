// Package config loads application settings from environment variables.
package config

import (
	"fmt"
	"os"
	"time"
)

const (
	defaultAddress           = ":8080"
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 15 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout   = 10 * time.Second
)

// Config contains HTTP server settings.
type Config struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

// Load reads configuration from environment variables.
func Load() (Config, error) {
	cfg := Config{
		Address:           envOrDefault("SERVER_ADDRESS", defaultAddress),
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
	}

	durationSettings := []struct {
		name   string
		target *time.Duration
	}{
		{name: "SERVER_READ_HEADER_TIMEOUT", target: &cfg.ReadHeaderTimeout},
		{name: "SERVER_READ_TIMEOUT", target: &cfg.ReadTimeout},
		{name: "SERVER_WRITE_TIMEOUT", target: &cfg.WriteTimeout},
		{name: "SERVER_IDLE_TIMEOUT", target: &cfg.IdleTimeout},
		{name: "SERVER_SHUTDOWN_TIMEOUT", target: &cfg.ShutdownTimeout},
	}

	for _, setting := range durationSettings {
		value := os.Getenv(setting.name)
		if value == "" {
			continue
		}

		duration, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", setting.name, err)
		}
		*setting.target = duration
	}

	return cfg, nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
