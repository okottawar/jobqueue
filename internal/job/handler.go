package job

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"time"
)

// Handler processes the payload of a single job type and returns an error
// if processing failed. Handlers must respect context cancellation.
type Handler func(ctx context.Context, payload json.RawMessage) error

// Registry maps job type names to their handler implementations.
type Registry struct {
	handlers map[string]Handler
}

// NewRegistry creates a registry pre-populated with the built-in demo
// handlers (email, webhook, report). Real deployments can register
// additional handlers via Register.
func NewRegistry() *Registry {
	r := &Registry{handlers: make(map[string]Handler)}
	r.Register("email", emailHandler)
	r.Register("webhook", webhookHandler)
	r.Register("report", reportHandler)
	return r
}

// Register adds or replaces the handler for a job type.
func (r *Registry) Register(jobType string, h Handler) {
	r.handlers[jobType] = h
}

// Get returns the handler for a job type, or false if none is registered.
func (r *Registry) Get(jobType string) (Handler, bool) {
	h, ok := r.handlers[jobType]
	return h, ok
}

// --- Built-in demo handlers -------------------------------------------------
//
// These simulate realistic asynchronous work (I/O latency, occasional
// transient failures) so the retry/backoff machinery has something to do
// during local testing and demos, without requiring external services.

func emailHandler(ctx context.Context, payload json.RawMessage) error {
	var body struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return fmt.Errorf("invalid email payload: %w", err)
	}
	if body.To == "" {
		return fmt.Errorf("email payload missing 'to' address")
	}
	return simulateWork(ctx, 300*time.Millisecond, 0.1)
}

func webhookHandler(ctx context.Context, payload json.RawMessage) error {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return fmt.Errorf("invalid webhook payload: %w", err)
	}
	if body.URL == "" {
		return fmt.Errorf("webhook payload missing 'url'")
	}
	return simulateWork(ctx, 500*time.Millisecond, 0.2)
}

func reportHandler(ctx context.Context, payload json.RawMessage) error {
	return simulateWork(ctx, 800*time.Millisecond, 0.05)
}

func simulateWork(ctx context.Context, base time.Duration, failRate float64) error {
	jitter := time.Duration(rand.Int63n(int64(base)))
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(base/2 + jitter):
	}
	if rand.Float64() < failRate {
		slog.Debug("simulated transient failure")
		return fmt.Errorf("simulated transient failure")
	}
	return nil
}
