package config

import (
	"os"
	"testing"
	"time"
)

func clearEnv(t *testing.T) {
	t.Helper()
	keys := []string{"PORT", "DATABASE_URL", "AUTH_USERNAME", "AUTH_PASSWORD", "WORKER_COUNT", "MAX_RETRIES", "QUEUE_SIZE", "POLL_INTERVAL"}
	for _, k := range keys {
		orig, had := os.LookupEnv(k)
		os.Unsetenv(k)
		t.Cleanup(func() {
			if had {
				os.Setenv(k, orig)
			}
		})
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	clearEnv(t)
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when DATABASE_URL is missing")
	}
}

func TestLoad_MissingAuthCredentials(t *testing.T) {
	clearEnv(t)
	os.Setenv("DATABASE_URL", "postgres://localhost/test")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when auth credentials are missing")
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)
	os.Setenv("DATABASE_URL", "postgres://localhost/test")
	os.Setenv("AUTH_USERNAME", "demo")
	os.Setenv("AUTH_PASSWORD", "change-me")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WorkerCount != 5 {
		t.Errorf("expected default worker count 5, got %d", cfg.WorkerCount)
	}
	if cfg.Port != "8080" {
		t.Errorf("expected default port 8080, got %s", cfg.Port)
	}
	if cfg.BaseBackoff != 2*time.Second {
		t.Errorf("expected default base backoff 2s, got %v", cfg.BaseBackoff)
	}
}

func TestLoad_InvalidWorkerCount(t *testing.T) {
	clearEnv(t)
	os.Setenv("DATABASE_URL", "postgres://localhost/test")
	os.Setenv("AUTH_USERNAME", "demo")
	os.Setenv("AUTH_PASSWORD", "change-me")
	os.Setenv("WORKER_COUNT", "0")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for WORKER_COUNT=0")
	}
}

func TestLoad_CustomValues(t *testing.T) {
	clearEnv(t)
	os.Setenv("DATABASE_URL", "postgres://localhost/test")
	os.Setenv("AUTH_USERNAME", "demo")
	os.Setenv("AUTH_PASSWORD", "change-me")
	os.Setenv("WORKER_COUNT", "10")
	os.Setenv("PORT", "9090")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WorkerCount != 10 || cfg.Port != "9090" {
		t.Errorf("expected custom values applied, got %+v", cfg)
	}
}
