package job

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// fakeStore is an in-memory implementation of the Store interface used to
// unit test the worker pool without a real database.
type fakeStore struct {
	mu sync.Mutex

	pending    []*Job
	completed  map[int64]int
	failed     map[int64]string
	retried    map[int64]int
	claimCalls int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		completed: make(map[int64]int),
		failed:    make(map[int64]string),
		retried:   make(map[int64]int),
	}
}

func (f *fakeStore) ClaimPendingJobs(ctx context.Context, limit int) ([]*Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimCalls++
	if len(f.pending) == 0 {
		return nil, nil
	}
	n := limit
	if n > len(f.pending) {
		n = len(f.pending)
	}
	claimed := f.pending[:n]
	f.pending = f.pending[n:]
	return claimed, nil
}

func (f *fakeStore) MarkCompleted(ctx context.Context, id int64, attempts int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed[id] = attempts
	return nil
}

func (f *fakeStore) MarkFailed(ctx context.Context, id int64, attempts int, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed[id] = errMsg
	return nil
}

func (f *fakeStore) ScheduleRetry(ctx context.Context, id int64, attempts int, errMsg string, nextRunAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retried[id]++
	return nil
}

func TestProcess_SuccessMarksCompleted(t *testing.T) {
	store := newFakeStore()
	registry := NewRegistry()
	registry.Register("noop", func(ctx context.Context, payload json.RawMessage) error {
		return nil
	})
	p := NewPool(PoolConfig{WorkerCount: 1}, store, registry)

	j := &Job{ID: 1, Type: "noop", Attempts: 0, MaxAttempts: 3, Payload: json.RawMessage(`{}`)}
	p.process(context.Background(), discardLogger(), j)

	store.mu.Lock()
	defer store.mu.Unlock()
	if attempts, ok := store.completed[1]; !ok || attempts != 1 {
		t.Errorf("expected job 1 marked completed with 1 attempt, got ok=%v attempts=%d", ok, attempts)
	}
}

func TestProcess_FailureSchedulesRetryUnderLimit(t *testing.T) {
	store := newFakeStore()
	registry := NewRegistry()
	registry.Register("alwaysfail", func(ctx context.Context, payload json.RawMessage) error {
		return errors.New("boom")
	})
	p := NewPool(PoolConfig{WorkerCount: 1, BaseBackoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond}, store, registry)

	j := &Job{ID: 2, Type: "alwaysfail", Attempts: 0, MaxAttempts: 3, Payload: json.RawMessage(`{}`)}
	p.process(context.Background(), discardLogger(), j)

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.retried[2] != 1 {
		t.Errorf("expected job 2 scheduled for retry once, got %d", store.retried[2])
	}
	if _, failed := store.failed[2]; failed {
		t.Error("job should not be marked failed before exhausting retries")
	}
}

func TestProcess_FailureExhaustsRetriesMarksFailed(t *testing.T) {
	store := newFakeStore()
	registry := NewRegistry()
	registry.Register("alwaysfail", func(ctx context.Context, payload json.RawMessage) error {
		return errors.New("boom")
	})
	p := NewPool(PoolConfig{WorkerCount: 1}, store, registry)

	// Attempts already at MaxAttempts-1, so this attempt exhausts retries.
	j := &Job{ID: 3, Type: "alwaysfail", Attempts: 2, MaxAttempts: 3, Payload: json.RawMessage(`{}`)}
	p.process(context.Background(), discardLogger(), j)

	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.failed[3]; !ok {
		t.Error("expected job 3 to be marked permanently failed")
	}
	if store.retried[3] != 0 {
		t.Error("job should not be scheduled for retry after exhausting attempts")
	}
}

func TestProcess_UnknownTypeMarksFailedImmediately(t *testing.T) {
	store := newFakeStore()
	registry := NewRegistry() // no "mystery" handler registered
	p := NewPool(PoolConfig{WorkerCount: 1}, store, registry)

	j := &Job{ID: 4, Type: "mystery", Attempts: 0, MaxAttempts: 3, Payload: json.RawMessage(`{}`)}
	p.process(context.Background(), discardLogger(), j)

	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.failed[4]; !ok {
		t.Error("expected job with unknown type to be marked failed")
	}
}

func TestBackoffFor_IncreasesAndCaps(t *testing.T) {
	p := NewPool(PoolConfig{WorkerCount: 1, BaseBackoff: time.Second, MaxBackoff: 5 * time.Second}, nil, nil)
	d1 := p.backoffFor(1)
	d3 := p.backoffFor(3)
	if d1 <= 0 {
		t.Fatal("expected positive backoff")
	}
	if d3 < d1 {
		t.Error("expected backoff to grow with attempt count")
	}
	d10 := p.backoffFor(10)
	// Capped at MaxBackoff plus up to 20% jitter.
	if d10 > 6*time.Second {
		t.Errorf("expected backoff to be capped near MaxBackoff, got %v", d10)
	}
}

// TestPoolRun_ConcurrentWorkersNoDuplicateProcessing verifies that when
// multiple workers run concurrently against a shared claim-based store, each
// job is processed exactly once — the store's atomic claim (simulated here
// by slicing under a mutex) prevents double dispatch.
func TestPoolRun_ConcurrentWorkersNoDuplicateProcessing(t *testing.T) {
	const numJobs = 50
	store := newFakeStore()
	for i := int64(1); i <= numJobs; i++ {
		store.pending = append(store.pending, &Job{
			ID: i, Type: "track", Attempts: 0, MaxAttempts: 1, Payload: json.RawMessage(`{}`),
		})
	}

	var mu sync.Mutex
	seen := make(map[int64]int)

	registry := NewRegistry()
	registry.Register("track", func(ctx context.Context, payload json.RawMessage) error {
		return nil
	})

	p := NewPool(PoolConfig{
		WorkerCount:  8,
		QueueSize:    numJobs,
		PollInterval: 5 * time.Millisecond,
	}, &trackingStore{fakeStore: store, onComplete: func(id int64) {
		mu.Lock()
		seen[id]++
		mu.Unlock()
	}}, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	p.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != numJobs {
		t.Fatalf("expected %d jobs processed, got %d", numJobs, len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("job %d processed %d times, expected exactly once", id, count)
		}
	}
}

// trackingStore wraps fakeStore to notify a callback on completion, letting
// tests observe exactly-once processing without racing on the fake's map.
type trackingStore struct {
	*fakeStore
	onComplete func(id int64)
}

func (t *trackingStore) MarkCompleted(ctx context.Context, id int64, attempts int) error {
	err := t.fakeStore.MarkCompleted(ctx, id, attempts)
	t.onComplete(id)
	return err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
