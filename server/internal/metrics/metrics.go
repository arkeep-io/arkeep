// Package metrics registers all Prometheus metrics for the Arkeep server.
//
// Usage:
//
//	m := metrics.New(prometheus.DefaultRegisterer)
//	metrics.RegisterAgentsGauge(prometheus.DefaultRegisterer, agentMgr.ConnectedAgentsCount)
//
// The handler to expose the /metrics endpoint is available via
// [metrics.Handler], which wraps prometheus.DefaultGatherer.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all custom Prometheus metric collectors.
type Metrics struct {
	// JobsTotal counts completed jobs, partitioned by terminal status and job type.
	// Incremented once per job on first terminal state (succeeded / failed / cancelled).
	JobsTotal *prometheus.CounterVec

	// JobDurationSeconds records job wall-clock duration in seconds for jobs
	// that have both started_at and ended_at timestamps.
	JobDurationSeconds *prometheus.HistogramVec

	// HTTPRequestsTotal counts HTTP requests by method, path pattern, and status code.
	// Updated by the HTTPMiddleware.
	HTTPRequestsTotal *prometheus.CounterVec

	// HTTPRequestDuration records HTTP request latency by method and path pattern.
	HTTPRequestDuration *prometheus.HistogramVec
}

// New registers and returns all custom metrics using the given Registerer.
// Call this once during server startup.
func New(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)

	return &Metrics{
		JobsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "arkeep_jobs_total",
			Help: "Total number of jobs that reached a terminal state, partitioned by status and type.",
		}, []string{"status", "job_type"}),

		JobDurationSeconds: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name: "arkeep_job_duration_seconds",
			Help: "Job execution wall-clock time in seconds (only recorded when both started_at and ended_at are known).",
			// Buckets from 1 s up to ~4.5 h in roughly doubling steps.
			Buckets: prometheus.ExponentialBuckets(1, 2, 14),
		}, []string{"job_type"}),

		HTTPRequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "arkeep_http_requests_total",
			Help: "Total number of HTTP requests handled, partitioned by method, route pattern, and status code.",
		}, []string{"method", "route", "status_code"}),

		HTTPRequestDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "arkeep_http_request_duration_seconds",
			Help:    "HTTP request latency in seconds, partitioned by method and route pattern.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
	}
}

// RegisterAgentsGauge registers a GaugeFunc that returns the number of currently
// connected agents. The provided fn is called on every Prometheus scrape.
func RegisterAgentsGauge(reg prometheus.Registerer, fn func() int) {
	promauto.With(reg).NewGaugeFunc(prometheus.GaugeOpts{
		Name: "arkeep_agents_connected",
		Help: "Number of agents currently holding an active gRPC StreamJobs connection.",
	}, func() float64 {
		return float64(fn())
	})
}

// RecordJob records a terminal job event.
// jobType should be one of "backup", "restore", "verify" (lower-case string from DB).
// startedAt / endedAt may be zero values — duration is only recorded when both are non-zero.
func (m *Metrics) RecordJob(status, jobType string, startedAt, endedAt time.Time) {
	m.JobsTotal.WithLabelValues(status, jobType).Inc()

	if !startedAt.IsZero() && !endedAt.IsZero() && endedAt.After(startedAt) {
		m.JobDurationSeconds.WithLabelValues(jobType).Observe(endedAt.Sub(startedAt).Seconds())
	}
}

// HTTPMiddleware returns a Chi-compatible middleware that records
// arkeep_http_requests_total and arkeep_http_request_duration_seconds.
//
// It must be mounted after chi/v5/middleware.RoutePattern so that
// chi.RouteContext(r.Context()).RoutePattern() returns the matched pattern.
func (m *Metrics) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		// chi.RouteContext is set by chi itself; fall back to request path
		// if no route was matched (e.g. static assets, /health).
		route := r.URL.Path
		if rc := routePattern(r); rc != "" {
			route = rc
		}

		statusStr := strconv.Itoa(rw.status)
		m.HTTPRequestsTotal.WithLabelValues(r.Method, route, statusStr).Inc()
		m.HTTPRequestDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

// Handler returns an http.Handler that serves the Prometheus text format for
// the default gatherer (includes Go runtime, process, and all registered metrics).
func Handler() http.Handler {
	return promhttp.Handler()
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// statusRecorder wraps http.ResponseWriter to capture the response status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// routePattern extracts the matched Chi route pattern from the request context.
// Returns an empty string if the chi routing context is not present (e.g. for
// static assets served before Chi routing runs).
func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		return rc.RoutePattern()
	}
	return ""
}
