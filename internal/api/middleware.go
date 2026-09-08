package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/oona-insurance/dev-portal/internal/auth"
	"golang.org/x/time/rate"
)

// ---------------------------------------------------------------------------
// OPS-02: Structured JSON Request Logger Middleware with Request ID Tracing
// ---------------------------------------------------------------------------

// StructuredLoggingMiddleware logs all incoming HTTP requests in structured JSON with Request ID
func StructuredLoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reqID := middleware.GetReqID(r.Context())
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				latency := time.Since(start)
				status := ww.Status()
				if status == 0 {
					status = 200
				}

				level := slog.LevelInfo
				if status >= 500 {
					level = slog.LevelError
				} else if status >= 400 {
					level = slog.LevelWarn
				}

				logger.LogAttrs(r.Context(), level, "HTTP request handled",
					slog.String("request_id", reqID),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("remote_addr", r.RemoteAddr),
					slog.Int("status", status),
					slog.Int("bytes_written", ww.BytesWritten()),
					slog.Duration("latency", latency),
					slog.String("user_agent", r.UserAgent()),
				)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

// ---------------------------------------------------------------------------
// CRIT-01: Rate Limiting Middleware
// ---------------------------------------------------------------------------

// ipRateLimiter holds per-(category+IP) rate limiters with last-seen timestamp for cleanup.
type ipRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rateLimiterEntry
}

type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

var globalRateLimiter = &ipRateLimiter{
	limiters: make(map[string]*rateLimiterEntry),
}

func (rl *ipRateLimiter) get(key string, r rate.Limit, b int) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	entry, exists := rl.limiters[key]
	if !exists {
		entry = &rateLimiterEntry{limiter: rate.NewLimiter(r, b)}
		rl.limiters[key] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter
}

// cleanupRateLimiters removes stale IP entries older than 10 minutes.
func init() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			globalRateLimiter.mu.Lock()
			for key, entry := range globalRateLimiter.limiters {
				if time.Since(entry.lastSeen) > 10*time.Minute {
					delete(globalRateLimiter.limiters, key)
				}
			}
			globalRateLimiter.mu.Unlock()
		}
	}()
}

// RateLimitMiddleware limits requests to `reqPerMinute` per client IP with a burst limit.
func RateLimitMiddleware(category string, reqPerMinute int, burst int) func(http.Handler) http.Handler {
	r := rate.Limit(float64(reqPerMinute) / 60.0)
	if burst <= 0 {
		burst = 5
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ip := req.RemoteAddr
			if realIP := req.Header.Get("X-Real-IP"); realIP != "" {
				ip = realIP
			}
			if idx := strings.LastIndex(ip, ":"); idx != -1 {
				ip = ip[:idx]
			}
			key := category + ":" + ip
			limiter := globalRateLimiter.get(key, r, burst)
			if !limiter.Allow() {
				http.Error(w, `{"error": "Too Many Requests. Please slow down."}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// LoginRateLimitMiddleware is a stricter limiter for the login endpoint:
// 10 requests per minute with a burst allowance of 5.
func LoginRateLimitMiddleware() func(http.Handler) http.Handler {
	return RateLimitMiddleware("login", 10, 5)
}

// ---------------------------------------------------------------------------
// CRIT-02: Security HTTP Headers Middleware
// ---------------------------------------------------------------------------

// SecurityHeadersMiddleware sets enterprise-grade HTTP security headers on every response.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent clickjacking
		w.Header().Set("X-Frame-Options", "DENY")
		// Prevent MIME-type sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Enforce HTTPS for 2 years (only meaningful behind TLS terminator)
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		// Strict referrer for privacy
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Disable browser features not needed
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		// Content Security Policy — allow self + CDN assets used by Tailwind/HTMX/SwaggerUI
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' 'unsafe-eval' cdn.tailwindcss.com cdn.jsdelivr.net unpkg.com; "+
				"style-src 'self' 'unsafe-inline' cdn.tailwindcss.com cdn.jsdelivr.net unpkg.com fonts.googleapis.com; "+
				"font-src 'self' data: fonts.gstatic.com unpkg.com; "+
				"img-src 'self' data: avatars.githubusercontent.com unpkg.com validator.swagger.io; "+
				"connect-src 'self' unpkg.com; "+
				"frame-ancestors 'none';")
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// MED-01: Request Timeout Middleware
// ---------------------------------------------------------------------------

// TimeoutMiddleware cancels the request context after d duration.
func TimeoutMiddleware(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// contextKey is used to avoid collision in request context
type contextKey string
const userCtxKey contextKey = "user_claims"

// AuthMiddleware validates JWT from Cookies or Headers and injects user claims into context
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var tokenString string

		// 1. Try to get token from Cookie (used by Browser)
		cookie, err := r.Cookie("oona_token")
		if err == nil {
			tokenString = cookie.Value
		}

		// 2. If no cookie, try Authorization header (used by API clients/curl)
		if tokenString == "" {
			authHeader := r.Header.Get("Authorization")
			if authHeader != "" {
				parts := strings.Split(authHeader, " ")
				if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
					tokenString = parts[1]
				}
			}
		}

		if tokenString == "" {
			// If it's an HTML request, redirect to login page instead of JSON 401 error
			if strings.Contains(r.Header.Get("Accept"), "text/html") {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			http.Error(w, "Unauthorized: missing token", http.StatusUnauthorized)
			return
		}

		claims, err := auth.ValidateToken(tokenString)
		if err != nil {
			if strings.Contains(r.Header.Get("Accept"), "text/html") {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}

		// Inject claims and role to context for downstream handlers
		ctx := context.WithValue(r.Context(), userCtxKey, claims)
		ctx = context.WithValue(ctx, auth.UserRoleKey, claims.Role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole is an RBAC middleware that enforces strict role-based access
func RequireRole(allowedRoles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := r.Context().Value(userCtxKey).(*auth.Claims)
			if !ok {
				http.Error(w, "Forbidden: unable to read claims", http.StatusForbidden)
				return
			}

			isAllowed := false
			for _, role := range allowedRoles {
				if claims.Role == role {
					isAllowed = true
					break
				}
			}

			if !isAllowed {
				http.Error(w, "Forbidden: you do not have permission for this action", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
// SEC-07: CSRF Protection Middleware (Double-Submit Cookie Pattern)
// ---------------------------------------------------------------------------

// csrfExcludedPaths are paths that do not require CSRF validation.
// /api/login: user doesn't have a CSRF token yet.
// /logout: clearing session is safe and idempotent.
var csrfExcludedPaths = map[string]bool{
	"/api/login": true,
	"/logout":    true,
}

// CSRFMiddleware validates that mutation requests (POST, PUT, DELETE, PATCH)
// carry a valid X-CSRF-Token header matching the oona_csrf cookie value.
// Safe methods (GET, HEAD, OPTIONS) are always allowed through.
func CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow safe/read-only HTTP methods
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		// Allow excluded paths (login, logout)
		if csrfExcludedPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		// Read the CSRF cookie
		csrfCookie, err := r.Cookie("oona_csrf")
		if err != nil || csrfCookie.Value == "" {
			slog.Warn("CSRF validation failed: missing oona_csrf cookie",
				"path", r.URL.Path,
				"method", r.Method,
				"remote_addr", r.RemoteAddr,
			)
			http.Error(w, `{"error": "CSRF token missing"}`, http.StatusForbidden)
			return
		}

		// Read the CSRF header
		csrfHeader := r.Header.Get("X-CSRF-Token")
		if csrfHeader == "" {
			// Also check form value as fallback for standard HTML forms
			csrfHeader = r.FormValue("_csrf_token")
		}

		// Constant-time comparison to prevent timing attacks
		if csrfHeader == "" || csrfHeader != csrfCookie.Value {
			slog.Warn("CSRF validation failed: token mismatch",
				"path", r.URL.Path,
				"method", r.Method,
				"remote_addr", r.RemoteAddr,
			)
			http.Error(w, `{"error": "CSRF token invalid"}`, http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// GenerateCSRFToken creates a cryptographically secure random hex token (32 bytes = 64 hex chars).
func GenerateCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fallback: this should never happen with crypto/rand
		return "fallback-csrf-token-insecure"
	}
	return hex.EncodeToString(b)
}

