package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/okottawar/jobqueue/internal/db"
	"github.com/okottawar/jobqueue/internal/job"
)

// JobStore is the subset of db.Store operations the API needs.
type JobStore interface {
	CreateJob(ctx context.Context, req job.CreateJobRequest) (*job.Job, error)
	GetJob(ctx context.Context, id int64) (*job.Job, error)
	ListJobs(ctx context.Context, status string, limit, offset int) ([]*job.Job, error)
	DeleteJob(ctx context.Context, id int64) error
	Stats(ctx context.Context) (*job.Stats, error)
}

// WorkerStats reports live worker pool information.
type WorkerStats interface {
	ActiveWorkers() int64
	WorkerCount() int
}

// Server holds dependencies for HTTP handlers.
type Server struct {
	store    JobStore
	registry *job.Registry
	pool     WorkerStats
}

// NewServer creates a new API server.
func NewServer(store JobStore, registry *job.Registry, pool WorkerStats) *Server {
	return &Server{store: store, registry: registry, pool: pool}
}

// Routes registers all HTTP routes on the given mux.
func (s *Server) Routes(mux *http.ServeMux, staticDir string) {
	mux.HandleFunc("GET /api/jobs", s.handleListJobs)
	mux.HandleFunc("POST /api/jobs", s.handleCreateJob)
	mux.HandleFunc("GET /api/jobs/{id}", s.handleGetJob)
	mux.HandleFunc("DELETE /api/jobs/{id}", s.handleDeleteJob)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /healthz", s.handleHealth)

	fs := http.FileServer(http.Dir(staticDir))
	mux.Handle("/", fs)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var req job.CreateJobRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "'type' is required")
		return
	}
	if _, ok := s.registry.Get(req.Type); !ok {
		writeError(w, http.StatusBadRequest, "unknown job type: "+req.Type)
		return
	}
	if req.MaxAttempts < 0 {
		writeError(w, http.StatusBadRequest, "'max_attempts' cannot be negative")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	j, err := s.store.CreateJob(ctx, req)
	if err != nil {
		slog.Error("failed to create job", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create job")
		return
	}
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" {
		if !isValidStatus(status) {
			writeError(w, http.StatusBadRequest, "invalid status filter: "+status)
			return
		}
	}
	limit := parseIntDefault(r.URL.Query().Get("limit"), 100)
	offset := parseIntDefault(r.URL.Query().Get("offset"), 0)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	jobs, err := s.store.ListJobs(ctx, status, limit, offset)
	if err != nil {
		slog.Error("failed to list jobs", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list jobs")
		return
	}
	if jobs == nil {
		jobs = []*job.Job{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	j, err := s.store.GetJob(ctx, id)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		slog.Error("failed to get job", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get job")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	err = s.store.DeleteJob(ctx, id)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		slog.Error("failed to delete job", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete job")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	st, err := s.store.Stats(ctx)
	if err != nil {
		slog.Error("failed to get stats", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get stats")
		return
	}
	if s.pool != nil {
		st.ActiveWorkers = s.pool.ActiveWorkers()
		st.WorkerCount = int64(s.pool.WorkerCount())
	}
	writeJSON(w, http.StatusOK, st)
}

func isValidStatus(status string) bool {
	switch job.Status(status) {
	case job.StatusPending, job.StatusProcessing, job.StatusCompleted, job.StatusFailed, job.StatusRetry:
		return true
	}
	return false
}

func parseIntDefault(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
