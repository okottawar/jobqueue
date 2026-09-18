package job

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRegistryBuiltins(t *testing.T) {
	r := NewRegistry()
	for _, typ := range []string{"email", "webhook", "report"} {
		if _, ok := r.Get(typ); !ok {
			t.Errorf("expected built-in handler for %q", typ)
		}
	}
	if _, ok := r.Get("nonexistent"); ok {
		t.Error("expected no handler for unregistered type")
	}
}

func TestRegistryRegisterCustom(t *testing.T) {
	r := NewRegistry()
	called := false
	r.Register("custom", func(ctx context.Context, payload json.RawMessage) error {
		called = true
		return nil
	})
	h, ok := r.Get("custom")
	if !ok {
		t.Fatal("expected custom handler to be registered")
	}
	if err := h(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected handler to be invoked")
	}
}

func TestEmailHandlerValidation(t *testing.T) {
	err := emailHandler(context.Background(), json.RawMessage(`{"subject":"hi"}`))
	if err == nil {
		t.Error("expected error for missing 'to' field")
	}
}

func TestWebhookHandlerValidation(t *testing.T) {
	err := webhookHandler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for missing 'url' field")
	}
}

func TestEmailHandlerInvalidJSON(t *testing.T) {
	err := emailHandler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON payload")
	}
}
