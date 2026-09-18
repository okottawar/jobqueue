package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration, sourced from environment
// variables with sensible defaults for local development.
type Config struct {
	Port          string
	DatabaseURL   string
	WorkerCount   int
	MaxRetries    int
	QueueSize     int
	PollInterval  time.Duration
	ShutdownGrace time.Duration
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	cfg := &Config{
		Port:          getEnv("PORT", "8080"),
		DatabaseURL:   getEnv("DATABASE_URL", ""),
		WorkerCount:   getEnvInt("WORKER_COUNT", 5),
		MaxRetries:    getEnvInt("MAX_RETRIES", 3),
		QueueSize:     getEnvInt("QUEUE_SIZE", 100),
		PollInterval:  getEnvDuration("POLL_INTERVAL", 2*time.Second),
		ShutdownGrace: getEnvDuration("SHUTDOWN_GRACE", 20*time.Second),
		BaseBackoff:   getEnvDuration("BASE_BACKOFF", 2*time.Second),
		MaxBackoff:    getEnvDuration("MAX_BACKOFF", 60*time.Second),
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL environment variable is required")
	}
	if cfg.WorkerCount < 1 {
		return nil, fmt.Errorf("WORKER_COUNT must be at least 1")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
