package api

import (
	"crypto/subtle"
	"net/http"
)

// WriteAuthMiddleware requires HTTP Basic Auth for API operations that change
// job data. Read-only endpoints remain public.
func WriteAuthMiddleware(username, password string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protected := (r.Method == http.MethodPost && (r.URL.Path == "/api/jobs" || r.URL.Path == "/api/jobs/batch")) ||
			(r.Method == http.MethodDelete && len(r.URL.Path) > len("/api/jobs/") && r.URL.Path[:len("/api/jobs/")] == "/api/jobs/")

		if !protected {
			next.ServeHTTP(w, r)
			return
		}

		providedUser, providedPassword, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(providedUser), []byte(username)) != 1 ||
			subtle.ConstantTimeCompare([]byte(providedPassword), []byte(password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Job Queue"`)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		next.ServeHTTP(w, r)
	})
}
