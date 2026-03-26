package api

import (
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	grpccerts "github.com/arkeep-io/arkeep/server/internal/grpc"
)

// EnrollHandler handles agent enrollment requests.
// Agents call POST /api/v1/agents/enroll to obtain a CA certificate and a
// signed client certificate that they then use for mTLS on the gRPC port.
type EnrollHandler struct {
	autoCerts   *grpccerts.AutoCerts
	agentSecret string
	logger      *zap.Logger

	// rate limiter: max 10 requests per minute per IP
	mu      sync.Mutex
	buckets map[string]*rateBucket
}

type rateBucket struct {
	count     int
	resetAt   time.Time
}

// NewEnrollHandler creates a new EnrollHandler.
// agentSecret is the shared bootstrap secret agents must present to enroll.
func NewEnrollHandler(autoCerts *grpccerts.AutoCerts, agentSecret string, logger *zap.Logger) *EnrollHandler {
	h := &EnrollHandler{
		autoCerts:   autoCerts,
		agentSecret: agentSecret,
		logger:      logger.Named("enroll_handler"),
		buckets:     make(map[string]*rateBucket),
	}
	// Periodically clean up stale buckets to avoid unbounded memory growth.
	go h.cleanupLoop()
	return h
}

// Enroll handles POST /api/v1/agents/enroll.
func (h *EnrollHandler) Enroll(w http.ResponseWriter, r *http.Request) {
	// Rate limiting
	ip := clientIP(r)
	if !h.allow(ip) {
		http.Error(w, "rate limit exceeded — max 10 enrollment requests per minute per IP", http.StatusTooManyRequests)
		return
	}

	var body struct {
		AgentSecret string `json:"agent_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if h.agentSecret != "" && body.AgentSecret != h.agentSecret {
		h.logger.Warn("enrollment rejected: wrong agent_secret", zap.String("ip", ip))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Issue a per-agent client certificate. The CN embeds a UUID so each
	// enrolled agent gets a unique certificate even if enrolled multiple times.
	cn := "arkeep-agent-" + uuid.New().String()
	certPEM, keyPEM, err := h.autoCerts.IssueCertificate(cn)
	if err != nil {
		h.logger.Error("failed to issue client certificate", zap.Error(err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Info("agent enrolled", zap.String("cn", cn), zap.String("ip", ip))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"ca_cert":     string(h.autoCerts.CACertPEM),
		"client_cert": string(certPEM),
		"client_key":  string(keyPEM),
	})
}

// allow returns true if the given IP has not exceeded the rate limit.
func (h *EnrollHandler) allow(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	b, ok := h.buckets[ip]
	if !ok || now.After(b.resetAt) {
		h.buckets[ip] = &rateBucket{count: 1, resetAt: now.Add(time.Minute)}
		return true
	}
	if b.count >= 10 {
		return false
	}
	b.count++
	return true
}

// cleanupLoop removes expired rate-limit buckets every minute.
func (h *EnrollHandler) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		h.mu.Lock()
		now := time.Now()
		for ip, b := range h.buckets {
			if now.After(b.resetAt) {
				delete(h.buckets, ip)
			}
		}
		h.mu.Unlock()
	}
}

// clientIP extracts the real client IP from the request, preferring the value
// set by middleware.RealIP (stored in RemoteAddr after chi processes it).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
