package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/okottawar/jobqueue/internal/job"
)

// ErrNotFound is returned when a requested job does not exist.
var ErrNotFound = errors.New("job not found")

// Store provides persistence operations for jobs.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new Store backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// CreateJob inserts a new job in the "pending" state and returns it.
func (s *Store) CreateJob(ctx context.Context, req job.CreateJobRequest) (*job.Job, error) {
	maxAttempts := req.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	payload := req.Payload
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}

	const q = `
		INSERT INTO jobs (type, payload, status, attempts, max_attempts, next_run_at)
		VALUES ($1, $2, 'pending', 0, $3, now())
		RETURNING id, type, payload, status, attempts, max_attempts, error,
		          created_at, started_at, completed_at, updated_at, next_run_at`

	row := s.pool.QueryRow(ctx, q, req.Type, payload, maxAttempts)
	return scanJob(row)
}

// CreateJobs inserts multiple jobs atomically in the "pending" state and returns them.
func (s *Store) CreateJobs(ctx context.Context, reqs []job.CreateJobRequest) ([]*job.Job, error) {
	if len(reqs) == 0 {
		return []*job.Job{}, nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning batch create transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	const q = `
		INSERT INTO jobs (type, payload, status, attempts, max_attempts, next_run_at)
		VALUES ($1, $2, 'pending', 0, $3, now())
		RETURNING id, type, payload, status, attempts, max_attempts, error,
		          created_at, started_at, completed_at, updated_at, next_run_at`

	jobs := make([]*job.Job, 0, len(reqs))
	for _, req := range reqs {
		maxAttempts := req.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 3
		}
		payload := req.Payload
		if payload == nil {
			payload = json.RawMessage(`{}`)
		}

		row := tx.QueryRow(ctx, q, req.Type, payload, maxAttempts)
		j, err := scanJob(row)
		if err != nil {
			return nil, fmt.Errorf("creating job in batch: %w", err)
		}
		jobs = append(jobs, j)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing batch create transaction: %w", err)
	}
	return jobs, nil
}

// GetJob fetches a single job by ID.
func (s *Store) GetJob(ctx context.Context, id int64) (*job.Job, error) {
	const q = `
		SELECT id, type, payload, status, attempts, max_attempts, error,
		       created_at, started_at, completed_at, updated_at, next_run_at
		FROM jobs WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	j, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return j, err
}

// ListJobs returns jobs, optionally filtered by status, newest first.
func (s *Store) ListJobs(ctx context.Context, status string, limit, offset int) ([]*job.Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows pgx.Rows
	var err error
	if status != "" {
		const q = `
			SELECT id, type, payload, status, attempts, max_attempts, error,
			       created_at, started_at, completed_at, updated_at, next_run_at
			FROM jobs WHERE status = $1
			ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		rows, err = s.pool.Query(ctx, q, status, limit, offset)
	} else {
		const q = `
			SELECT id, type, payload, status, attempts, max_attempts, error,
			       created_at, started_at, completed_at, updated_at, next_run_at
			FROM jobs ORDER BY created_at DESC LIMIT $1 OFFSET $2`
		rows, err = s.pool.Query(ctx, q, limit, offset)
	}
	if err != nil {
		return nil, fmt.Errorf("querying jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*job.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// DeleteJob removes a job by ID. Returns ErrNotFound if it doesn't exist.
func (s *Store) DeleteJob(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM jobs WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimPendingJobs atomically claims up to `limit` jobs that are due for
// processing (pending or retry, with next_run_at in the past), marking them
// as "processing" so no two workers/instances can pick up the same job.
// Uses FOR UPDATE SKIP LOCKED for safe concurrent claiming.
func (s *Store) ClaimPendingJobs(ctx context.Context, limit int) ([]*job.Job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	const selectQ = `
		SELECT id FROM jobs
		WHERE status IN ('pending', 'retry') AND next_run_at <= now()
		ORDER BY created_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED`
	rows, err := tx.Query(ctx, selectQ, limit)
	if err != nil {
		return nil, fmt.Errorf("selecting claimable jobs: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, tx.Commit(ctx)
	}

	const updateQ = `
		UPDATE jobs SET status = 'processing', started_at = now(), updated_at = now()
		WHERE id = ANY($1)
		RETURNING id, type, payload, status, attempts, max_attempts, error,
		          created_at, started_at, completed_at, updated_at, next_run_at`
	updRows, err := tx.Query(ctx, updateQ, ids)
	if err != nil {
		return nil, fmt.Errorf("claiming jobs: %w", err)
	}
	var claimed []*job.Job
	for updRows.Next() {
		j, err := scanJob(updRows)
		if err != nil {
			updRows.Close()
			return nil, err
		}
		claimed = append(claimed, j)
	}
	updRows.Close()

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing claim tx: %w", err)
	}
	return claimed, nil
}

// MarkCompleted transitions a job to "completed".
func (s *Store) MarkCompleted(ctx context.Context, id int64, attempts int) error {
	const q = `
		UPDATE jobs SET status = 'completed', attempts = $2, error = NULL,
		       completed_at = now(), updated_at = now()
		WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, attempts)
	return err
}

// MarkFailed transitions a job to "failed" (retries exhausted).
func (s *Store) MarkFailed(ctx context.Context, id int64, attempts int, errMsg string) error {
	const q = `
		UPDATE jobs SET status = 'failed', attempts = $2, error = $3,
		       completed_at = now(), updated_at = now()
		WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, attempts, errMsg)
	return err
}

// ScheduleRetry transitions a job back to "retry" with a next_run_at delay.
func (s *Store) ScheduleRetry(ctx context.Context, id int64, attempts int, errMsg string, nextRunAt time.Time) error {
	const q = `
		UPDATE jobs SET status = 'retry', attempts = $2, error = $3,
		       next_run_at = $4, updated_at = now()
		WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, attempts, errMsg, nextRunAt)
	return err
}

// ResetStuckProcessing resets any jobs left "processing" (e.g. after an
// unclean shutdown) back to "retry" so they get picked up again. Intended
// to be run once at startup.
func (s *Store) ResetStuckProcessing(ctx context.Context) (int64, error) {
	const q = `
		UPDATE jobs SET status = 'retry', next_run_at = now(), updated_at = now()
		WHERE status = 'processing'`
	tag, err := s.pool.Exec(ctx, q)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Stats computes aggregate counts for the dashboard/API.
func (s *Store) Stats(ctx context.Context) (*job.Stats, error) {
	const q = `
		SELECT
			COUNT(*) FILTER (WHERE TRUE) AS total,
			COUNT(*) FILTER (WHERE status = 'pending') AS pending,
			COUNT(*) FILTER (WHERE status = 'processing') AS processing,
			COUNT(*) FILTER (WHERE status = 'completed') AS completed,
			COUNT(*) FILTER (WHERE status = 'failed') AS failed
		FROM jobs`
	var st job.Stats
	err := s.pool.QueryRow(ctx, q).Scan(&st.TotalJobs, &st.PendingJobs, &st.ProcessingJobs, &st.CompletedJobs, &st.FailedJobs)
	if err != nil {
		return nil, fmt.Errorf("querying stats: %w", err)
	}
	return &st, nil
}

// rowScanner abstracts pgx.Row / pgx.Rows for shared scanning logic.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (*job.Job, error) {
	var j job.Job
	err := row.Scan(
		&j.ID, &j.Type, &j.Payload, &j.Status, &j.Attempts, &j.MaxAttempts, &j.Error,
		&j.CreatedAt, &j.StartedAt, &j.CompletedAt, &j.UpdatedAt, &j.NextRunAt,
	)
	if err != nil {
		return nil, err
	}
	return &j, nil
}
