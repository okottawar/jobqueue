package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteAuthMiddleware_AllowsPublicRead(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := WriteAuthMiddleware("demo", "secret", next)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected public GET to pass, got %d", w.Code)
	}
}

func TestWriteAuthMiddleware_RejectsWriteWithoutCredentials(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("protected handler should not be called")
	})
	h := WriteAuthMiddleware("demo", "secret", next)

	req := httptest.NewRequest(http.MethodPost, "/api/jobs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatal("expected WWW-Authenticate header")
	}
}

func TestWriteAuthMiddleware_RejectsWrongCredentials(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("protected handler should not be called")
	})
	h := WriteAuthMiddleware("demo", "secret", next)

	req := httptest.NewRequest(http.MethodPost, "/api/jobs", nil)
	req.SetBasicAuth("demo", "wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestWriteAuthMiddleware_AllowsValidWriteCredentials(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	h := WriteAuthMiddleware("demo", "secret", next)

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/batch", nil)
	req.SetBasicAuth("demo", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Fatal("expected protected handler to be called")
	}
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestWriteAuthMiddleware_ProtectsDelete(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := WriteAuthMiddleware("demo", "secret", next)

	req := httptest.NewRequest(http.MethodDelete, "/api/jobs/42", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
