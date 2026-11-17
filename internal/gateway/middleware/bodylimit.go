package middleware

import "net/http"

// MaxBody caps the request body at n bytes. Reads past the cap fail with
// *http.MaxBytesError, which handlers map to 413.
func MaxBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}
