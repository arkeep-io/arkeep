package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestHTTPMiddleware_ExposesResponseController checks that handlers behind the
// middleware can still extend their write deadline and flush — long snapshot
// browses and streamed downloads depend on both.
func TestHTTPMiddleware_ExposesResponseController(t *testing.T) {
	// Flush sends the response before the handler returns, so the results
	// travel over a channel rather than shared variables.
	errs := make(chan [2]error, 1)
	handler := New(prometheus.NewRegistry()).HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rc := http.NewResponseController(w)
		deadlineErr := rc.SetWriteDeadline(time.Now().Add(time.Minute))
		errs <- [2]error{deadlineErr, rc.Flush()}
	}))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()

	got := <-errs
	if deadlineErr := got[0]; deadlineErr != nil {
		t.Errorf("SetWriteDeadline through the middleware: %v", deadlineErr)
	}
	if flushErr := got[1]; flushErr != nil {
		t.Errorf("Flush through the middleware: %v", flushErr)
	}
}

// TestHTTPMiddleware_BoundedLabels guards against unauthenticated clients
// creating a new time series per request (SEC-36): paths that match no route
// and non-standard methods collapse into one label value each.
func TestHTTPMiddleware_BoundedLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg)
	handler := m.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/random-1", nil),
		httptest.NewRequest(http.MethodGet, "/random-2", nil),
		httptest.NewRequest("BREW", "/random-3", nil),
		httptest.NewRequest("PURGE", "/random-4", nil),
	} {
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	if got := testutil.CollectAndCount(m.HTTPRequestsTotal); got != 2 {
		t.Errorf("arkeep_http_requests_total has %d series, want 2 (GET and OTHER on the unmatched route)", got)
	}
	if got := testutil.ToFloat64(m.HTTPRequestsTotal.WithLabelValues(http.MethodGet, "unmatched", "404")); got != 2 {
		t.Errorf("GET unmatched 404 = %v, want 2", got)
	}
}
