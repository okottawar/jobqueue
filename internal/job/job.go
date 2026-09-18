package job

import (
	"encoding/json"
	"time"
)

// Status represents the lifecycle state of a job.
type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusRetry      Status = "retry"
)

// Job represents a single unit of asynchronous work.
type Job struct {
	ID          int64           `json:"id"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	Status      Status          `json:"status"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	Error       *string         `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at"`
	NextRunAt   time.Time       `json:"next_run_at"`
}

// CreateJobRequest is the payload accepted by POST /api/jobs.
type CreateJobRequest struct {
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	MaxAttempts int             `json:"max_attempts,omitempty"`
}

// Stats summarizes the current state of the job system.
type Stats struct {
	TotalJobs      int64 `json:"total_jobs"`
	PendingJobs    int64 `json:"pending_jobs"`
	ProcessingJobs int64 `json:"processing_jobs"`
	CompletedJobs  int64 `json:"completed_jobs"`
	FailedJobs     int64 `json:"failed_jobs"`
	ActiveWorkers  int64 `json:"active_workers"`
	WorkerCount    int64 `json:"worker_count"`
}
