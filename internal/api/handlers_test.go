package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/okottawar/jobqueue/internal/db"
	"github.com/okottawar/jobqueue/internal/job"
)

// fakeJobStore is an in-memory JobStore for testing API handlers in
// isolation from PostgreSQL.
type fakeJobStore struct {
	jobs   map[int64]*job.Job
	nextID int64
}

func newFakeJobStore() *fakeJobStore {
	return &fakeJobStore{jobs: make(map[int64]*job.Job)}
}

func (f *fakeJobStore) CreateJob(ctx context.Context, req job.CreateJobRequest) (*job.Job, error) {
	f.nextID++
	maxAttempts := req.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	j := &job.Job{
		ID:          f.nextID,
		Type:        req.Type,
		Payload:     req.Payload,
		Status:      job.StatusPending,
		MaxAttempts: maxAttempts,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	f.jobs[j.ID] = j
	return j, nil
}

func (f *fakeJobStore) CreateJobs(ctx context.Context, reqs []job.CreateJobRequest) ([]*job.Job, error) {
	jobs := make([]*job.Job, 0, len(reqs))
	for _, req := range reqs {
		created, err := f.CreateJob(ctx, req)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, created)
	}
	return jobs, nil
}

func (f *fakeJobStore) GetJob(ctx context.Context, id int64) (*job.Job, error) {
	j, ok := f.jobs[id]
	if !ok {
		return nil, db.ErrNotFound
	}
	return j, nil
}

func (f *fakeJobStore) ListJobs(ctx context.Context, status string, limit, offset int) ([]*job.Job, error) {
	var out []*job.Job
	for _, j := range f.jobs {
		if status == "" || string(j.Status) == status {
			out = append(out, j)
		}
	}
	return out, nil
}

func (f *fakeJobStore) DeleteJob(ctx context.Context, id int64) error {
	if _, ok := f.jobs[id]; !ok {
		return db.ErrNotFound
	}
	delete(f.jobs, id)
	return nil
}

func (f *fakeJobStore) Stats(ctx context.Context) (*job.Stats, error) {
	st := &job.Stats{}
	for _, j := range f.jobs {
		st.TotalJobs++
		switch j.Status {
		case job.StatusPending:
			st.PendingJobs++
		case job.StatusProcessing:
			st.ProcessingJobs++
		case job.StatusCompleted:
			st.CompletedJobs++
		case job.StatusFailed:
			st.FailedJobs++
		}
	}
	return st, nil
}

type fakeWorkerStats struct{}

func (fakeWorkerStats) ActiveWorkers() int64 { return 2 }
func (fakeWorkerStats) WorkerCount() int     { return 5 }

func newTestServer() (*Server, *http.ServeMux) {
	store := newFakeJobStore()
	registry := job.NewRegistry()
	s := NewServer(store, registry, fakeWorkerStats{})
	mux := http.NewServeMux()
	s.Routes(mux, "/tmp") // static dir irrelevant for API tests
	return s, mux
}

func TestCreateJob_Success(t *testing.T) {
	_, mux := newTestServer()
	body := `{"type":"email","payload":{"to":"a@b.com"},"max_attempts":5}`
	req := httptest.NewRequest(http.MethodPost, "/api/jobs", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var j job.Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if j.Type != "email" || j.MaxAttempts != 5 {
		t.Errorf("unexpected job: %+v", j)
	}
}

func TestCreateJob_MissingType(t *testing.T) {
	_, mux := newTestServer()
	body := `{"payload":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/jobs", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateJob_UnknownType(t *testing.T) {
	_, mux := newTestServer()
	body := `{"type":"not-a-real-type","payload":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/jobs", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown job type, got %d", w.Code)
	}
}

func TestCreateJobs_Success(t *testing.T) {
	s, mux := newTestServer()
	body := `{"jobs":[
		{"type":"report","payload":{},"max_attempts":3},
		{"type":"email","payload":{"to":"a@b.com"},"max_attempts":4},
		{"type":"webhook","payload":{"url":"https://example.com"}}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/batch", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var response job.BatchCreateJobsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(response.Jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(response.Jobs))
	}
	if len(s.store.jobs) != 3 {
		t.Fatalf("expected 3 stored jobs, got %d", len(s.store.jobs))
	}
	for _, created := range response.Jobs {
		if created.ID == 0 || created.Status != job.StatusPending || created.Attempts != 0 {
			t.Errorf("unexpected created job: %+v", created)
		}
	}
}

func TestCreateJobs_EmptyBatch(t *testing.T) {
	s, mux := newTestServer()
	body := `{"jobs":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/batch", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if len(s.store.jobs) != 0 {
		t.Fatalf("expected no jobs to be stored, got %d", len(s.store.jobs))
	}
}

func TestCreateJobs_BatchTooLarge(t *testing.T) {
	s, mux := newTestServer()
	var jobs []job.CreateJobRequest
	for i := 0; i < 101; i++ {
		jobs = append(jobs, job.CreateJobRequest{Type: "report"})
	}
	bodyBytes, err := json.Marshal(job.BatchCreateJobsRequest{Jobs: jobs})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/batch", strings.NewReader(string(bodyBytes)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if len(s.store.jobs) != 0 {
		t.Fatalf("expected no jobs to be stored, got %d", len(s.store.jobs))
	}
}

func TestCreateJobs_InvalidJobTypeDoesNotCreateAnyJobs(t *testing.T) {
	s, mux := newTestServer()
	body := `{"jobs":[
		{"type":"report","payload":{}},
		{"type":"not-a-real-type","payload":{}},
		{"type":"report","payload":{}}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/batch", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if len(s.store.jobs) != 0 {
		t.Fatalf("expected no jobs to be stored, got %d", len(s.store.jobs))
	}
}

func TestCreateJobs_InvalidMaxAttemptsDoesNotCreateAnyJobs(t *testing.T) {
	s, mux := newTestServer()
	body := `{"jobs":[
		{"type":"report","payload":{}},
		{"type":"report","payload":{},"max_attempts":-1}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/batch", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if len(s.store.jobs) != 0 {
		t.Fatalf("expected no jobs to be stored, got %d", len(s.store.jobs))
	}
}

func TestGetJob_NotFound(t *testing.T) {
	_, mux := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/999", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetJob_Success(t *testing.T) {
	s, mux := newTestServer()
	created, _ := s.store.CreateJob(context.Background(), job.CreateJobRequest{Type: "email"})

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+itoa(created.ID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestDeleteJob(t *testing.T) {
	s, mux := newTestServer()
	created, _ := s.store.CreateJob(context.Background(), job.CreateJobRequest{Type: "email"})

	req := httptest.NewRequest(http.MethodDelete, "/api/jobs/"+itoa(created.ID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	// Second delete should 404.
	req2 := httptest.NewRequest(http.MethodDelete, "/api/jobs/"+itoa(created.ID), nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on second delete, got %d", w2.Code)
	}
}

func TestListJobs_InvalidStatusFilter(t *testing.T) {
	_, mux := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/jobs?status=bogus", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestStatsEndpoint_IncludesWorkerInfo(t *testing.T) {
	_, mux := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var st job.Stats
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("failed to decode stats: %v", err)
	}
	if st.ActiveWorkers != 2 || st.WorkerCount != 5 {
		t.Errorf("expected worker stats from pool, got %+v", st)
	}
}

func TestHealthEndpoint(t *testing.T) {
	_, mux := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
