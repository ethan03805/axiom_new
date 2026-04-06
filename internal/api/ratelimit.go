package api

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimiter implements per-token sliding window rate limiting.
// Per Section 24.3: rate_limit_rpm requests per minute per token.
type RateLimiter struct {
	rpm     int
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens    []time.Time
	lastClean time.Time
}

// NewRateLimiter creates a rate limiter with the given requests-per-minute limit.
// A limit of 0 disables rate limiting.
func NewRateLimiter(rpm int) *RateLimiter {
	return &RateLimiter{
		rpm:     rpm,
		buckets: make(map[string]*bucket),
	}
}

// Middleware returns HTTP middleware that enforces the rate limit.
// Keyed by the Authorization header value (per-token).
func (rl *RateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rl.rpm <= 0 {
				next.ServeHTTP(w, r)
				return
			}

			key := r.Header.Get("Authorization")
			if key == "" {
				key = r.RemoteAddr
			}

			if !rl.allow(key) {
				w.Header().Set("Retry-After", strconv.Itoa(60))
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (rl *RateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	window := now.Add(-1 * time.Minute)

	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{lastClean: now}
		rl.buckets[key] = b
	}

	// Clean expired entries
	if now.Sub(b.lastClean) > 10*time.Second {
		clean := b.tokens[:0]
		for _, t := range b.tokens {
			if t.After(window) {
				clean = append(clean, t)
			}
		}
		b.tokens = clean
		b.lastClean = now
	}

	// Count requests in window
	count := 0
	for _, t := range b.tokens {
		if t.After(window) {
			count++
		}
	}

	if count >= rl.rpm {
		return false
	}

	b.tokens = append(b.tokens, now)
	return true
}

// IPAllowlist returns middleware that restricts access to the listed IPs/CIDRs.
// An empty or nil list allows all connections.
// Per Section 24.3: optional IP restrictions.
func IPAllowlist(allowed []string) func(http.Handler) http.Handler {
	if len(allowed) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	var nets []*net.IPNet
	var ips []net.IP

	for _, entry := range allowed {
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			nets = append(nets, cidr)
		} else if ip := net.ParseIP(entry); ip != nil {
			ips = append(ips, ip)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			clientIP := net.ParseIP(host)
			if clientIP == nil {
				writeError(w, http.StatusForbidden, "cannot determine client IP")
				return
			}

			for _, ip := range ips {
				if ip.Equal(clientIP) {
					next.ServeHTTP(w, r)
					return
				}
			}
			for _, cidr := range nets {
				if cidr.Contains(clientIP) {
					next.ServeHTTP(w, r)
					return
				}
			}

			writeError(w, http.StatusForbidden, "IP not in allowlist")
		})
	}
}
