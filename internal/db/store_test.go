package db

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/okottawar/jobqueue/internal/job"
)

// These tests exercise the real PostgreSQL-backed Store. They are skipped
// automatically unless TEST_DATABASE_URL is set (e.g. in CI via
// `docker compose up postgres` or a Supabase test project), so `go test
// ./...` works out of the box without a database.
func setupTestStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set, skipping database integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := Connect(ctx, url)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	t.Cleanup(pool.Close)

	migrationSQL, err := os.ReadFile("../../migrations/001_init.sql")
	if err != nil {
		t.Fatalf("failed to read migration: %v", err)
	}
	if err := Migrate(ctx, pool, string(migrationSQL)); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	if _, err := pool.Exec(ctx, "TRUNCATE jobs RESTART IDENTITY"); err != nil {
		t.Fatalf("failed to truncate jobs table: %v", err)
	}

	return NewStore(pool)
}

func TestStore_CreateAndGetJob(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	created, err := store.CreateJob(ctx, job.CreateJobRequest{
		Type:    "email",
		Payload: json.RawMessage(`{"to":"a@b.com"}`),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if created.Status != job.StatusPending {
		t.Errorf("expected pending status, got %s", created.Status)
	}

	fetched, err := store.GetJob(ctx, created.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if fetched.ID != created.ID || fetched.Type != "email" {
		t.Errorf("fetched job mismatch: %+v", fetched)
	}
}

func TestStore_GetJob_NotFound(t *testing.T) {
	store := setupTestStore(t)
	_, err := store.GetJob(context.Background(), 999999)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_ClaimPendingJobs_NoDoubleClaim(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		if _, err := store.CreateJob(ctx, job.CreateJobRequest{Type: "email"}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
	}

	batch1, err := store.ClaimPendingJobs(ctx, 5)
	if err != nil {
		t.Fatalf("claim 1 failed: %v", err)
	}
	batch2, err := store.ClaimPendingJobs(ctx, 10)
	if err != nil {
		t.Fatalf("claim 2 failed: %v", err)
	}

	if len(batch1) != 5 {
		t.Errorf("expected 5 jobs in first batch, got %d", len(batch1))
	}
	if len(batch2) != 5 {
		t.Errorf("expected remaining 5 jobs in second batch, got %d", len(batch2))
	}

	seen := make(map[int64]bool)
	for _, j := range append(batch1, batch2...) {
		if seen[j.ID] {
			t.Errorf("job %d claimed twice", j.ID)
		}
		seen[j.ID] = true
		if j.Status != job.StatusProcessing {
			t.Errorf("expected claimed job to be processing, got %s", j.Status)
		}
	}
}

func TestStore_RetryAndFailureLifecycle(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	created, err := store.CreateJob(ctx, job.CreateJobRequest{Type: "email", MaxAttempts: 2})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	claimed, err := store.ClaimPendingJobs(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim failed: %v (got %d jobs)", err, len(claimed))
	}

	// First failure -> retry.
	if err := store.ScheduleRetry(ctx, created.ID, 1, "transient error", time.Now()); err != nil {
		t.Fatalf("schedule retry failed: %v", err)
	}
	afterRetry, err := store.GetJob(ctx, created.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if afterRetry.Status != job.StatusRetry {
		t.Errorf("expected retry status, got %s", afterRetry.Status)
	}

	// Claim again and mark permanently failed (exhausted retries).
	claimed2, err := store.ClaimPendingJobs(ctx, 1)
	if err != nil || len(claimed2) != 1 {
		t.Fatalf("second claim failed: %v", err)
	}
	if err := store.MarkFailed(ctx, created.ID, 2, "permanent error"); err != nil {
		t.Fatalf("mark failed error: %v", err)
	}
	final, err := store.GetJob(ctx, created.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if final.Status != job.StatusFailed {
		t.Errorf("expected failed status, got %s", final.Status)
	}
	if final.Error == nil || *final.Error != "permanent error" {
		t.Errorf("expected error message preserved, got %v", final.Error)
	}
}

func TestStore_DeleteJob(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	created, _ := store.CreateJob(ctx, job.CreateJobRequest{Type: "email"})
	if err := store.DeleteJob(ctx, created.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if err := store.DeleteJob(ctx, created.ID); err != ErrNotFound {
		t.Errorf("expected ErrNotFound on second delete, got %v", err)
	}
}

func TestStore_Stats(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		store.CreateJob(ctx, job.CreateJobRequest{Type: "email"})
	}
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if stats.TotalJobs != 3 || stats.PendingJobs != 3 {
		t.Errorf("unexpected stats: %+v", stats)
	}
}
