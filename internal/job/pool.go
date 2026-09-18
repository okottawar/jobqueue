package job

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// Store is the subset of persistence operations the worker pool needs.
// Defined here (consumer side) so internal/db has no dependency on internal/job's
// concurrency internals, keeping the packages decoupled.
type Store interface {
	ClaimPendingJobs(ctx context.Context, limit int) ([]*Job, error)
	MarkCompleted(ctx context.Context, id int64, attempts int) error
	MarkFailed(ctx context.Context, id int64, attempts int, errMsg string) error
	ScheduleRetry(ctx context.Context, id int64, attempts int, errMsg string, nextRunAt time.Time) error
}

// PoolConfig configures a worker Pool.
type PoolConfig struct {
	WorkerCount  int
	QueueSize    int
	PollInterval time.Duration
	BaseBackoff  time.Duration
	MaxBackoff   time.Duration
}

// Pool is a fixed-size worker pool that pulls due jobs from the Store,
// dispatches them over a buffered channel, and processes them concurrently.
type Pool struct {
	cfg      PoolConfig
	store    Store
	registry *Registry

	queue chan *Job

	activeWorkers atomic.Int64
	wg            sync.WaitGroup

	stopPoll chan struct{}
}

// NewPool creates a new worker pool.
func NewPool(cfg PoolConfig, store Store, registry *Registry) *Pool {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = cfg.WorkerCount * 4
	}
	return &Pool{
		cfg:      cfg,
		store:    store,
		registry: registry,
		queue:    make(chan *Job, cfg.QueueSize),
		stopPoll: make(chan struct{}),
	}
}

// ActiveWorkers returns the number of workers currently processing a job.
func (p *Pool) ActiveWorkers() int64 {
	return p.activeWorkers.Load()
}

// WorkerCount returns the configured pool size.
func (p *Pool) WorkerCount() int {
	return p.cfg.WorkerCount
}

// Run starts the poller and worker goroutines. It blocks until ctx is
// cancelled, at which point it stops polling for new jobs, waits for
// in-flight jobs to finish (or the context's grace period to elapse), and
// returns.
func (p *Pool) Run(ctx context.Context) {
	for i := 0; i < p.cfg.WorkerCount; i++ {
		p.wg.Add(1)
		go p.worker(ctx, i)
	}

	p.wg.Add(1)
	go p.poller(ctx)

	<-ctx.Done()
	slog.Info("worker pool shutting down, waiting for in-flight jobs")
	close(p.stopPoll)
	p.wg.Wait()
	slog.Info("worker pool stopped")
}

// poller periodically claims due jobs from the store and pushes them onto
// the in-memory queue for workers to pick up.
func (p *Pool) poller(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopPoll:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.claimAndEnqueue(ctx)
		}
	}
}

func (p *Pool) claimAndEnqueue(ctx context.Context) {
	// Only claim as many as we have queue headroom for.
	headroom := cap(p.queue) - len(p.queue)
	if headroom <= 0 {
		return
	}
	claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	jobs, err := p.store.ClaimPendingJobs(claimCtx, headroom)
	if err != nil {
		slog.Error("failed to claim pending jobs", "error", err)
		return
	}
	for _, j := range jobs {
		select {
		case p.queue <- j:
		case <-ctx.Done():
			return
		}
	}
	if len(jobs) > 0 {
		slog.Info("claimed jobs for processing", "count", len(jobs))
	}
}

// worker pulls jobs off the queue and processes them until the queue is
// closed/drained and shutdown has been requested.
func (p *Pool) worker(ctx context.Context, id int) {
	defer p.wg.Done()
	log := slog.With("worker_id", id)
	log.Info("worker started")

	for {
		select {
		case j, ok := <-p.queue:
			if !ok {
				return
			}
			p.activeWorkers.Add(1)
			p.process(ctx, log, j)
			p.activeWorkers.Add(-1)
		case <-p.stopPoll:
			// Drain any remaining buffered jobs before exiting, using a
			// bounded grace period derived from the parent context.
			select {
			case j, ok := <-p.queue:
				if !ok {
					return
				}
				p.activeWorkers.Add(1)
				p.process(ctx, log, j)
				p.activeWorkers.Add(-1)
			default:
				log.Info("worker stopping, no more queued jobs")
				return
			}
		}
	}
}

func (p *Pool) process(ctx context.Context, log *slog.Logger, j *Job) {
	handler, ok := p.registry.Get(j.Type)
	if !ok {
		errMsg := fmt.Sprintf("no handler registered for job type %q", j.Type)
		log.Error("unknown job type", "job_id", j.ID, "type", j.Type)
		_ = p.store.MarkFailed(ctx, j.ID, j.Attempts, errMsg)
		return
	}

	attempts := j.Attempts + 1
	log.Info("processing job", "job_id", j.ID, "type", j.Type, "attempt", attempts)

	// Give each job execution a bounded timeout so a single hanging job
	// cannot stall the whole pool, but respect the parent shutdown context.
	jobCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	start := time.Now()
	err := handler(jobCtx, j.Payload)
	duration := time.Since(start)

	if err == nil {
		log.Info("job completed", "job_id", j.ID, "duration_ms", duration.Milliseconds())
		if err := p.store.MarkCompleted(ctx, j.ID, attempts); err != nil {
			log.Error("failed to mark job completed", "job_id", j.ID, "error", err)
		}
		return
	}

	log.Warn("job failed", "job_id", j.ID, "attempt", attempts, "error", err)
	if attempts >= j.MaxAttempts {
		if ferr := p.store.MarkFailed(ctx, j.ID, attempts, err.Error()); ferr != nil {
			log.Error("failed to mark job failed", "job_id", j.ID, "error", ferr)
		}
		return
	}

	backoff := p.backoffFor(attempts)
	nextRun := time.Now().Add(backoff)
	if rerr := p.store.ScheduleRetry(ctx, j.ID, attempts, err.Error(), nextRun); rerr != nil {
		log.Error("failed to schedule retry", "job_id", j.ID, "error", rerr)
	} else {
		log.Info("job scheduled for retry", "job_id", j.ID, "attempt", attempts, "retry_in", backoff)
	}
}

// backoffFor computes exponential backoff with jitter: base * 2^(attempt-1),
// capped at MaxBackoff, plus up to 20% random jitter to avoid thundering herd.
func (p *Pool) backoffFor(attempt int) time.Duration {
	base := p.cfg.BaseBackoff
	if base <= 0 {
		base = 2 * time.Second
	}
	max := p.cfg.MaxBackoff
	if max <= 0 {
		max = 60 * time.Second
	}
	d := time.Duration(float64(base) * math.Pow(2, float64(attempt-1)))
	if d > max {
		d = max
	}
	jitter := time.Duration(rand.Int63n(int64(d)/5 + 1)) // up to ~20%
	return d + jitter
}
