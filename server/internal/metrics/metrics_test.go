package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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
