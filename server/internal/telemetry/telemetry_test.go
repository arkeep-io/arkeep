package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"testing"

	"go.uber.org/zap"
)

func TestLoadOrCreateID_CreatesFileOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry_id")

	id, created, err := loadOrCreateID(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Error("expected created=true on first run")
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(data) != id {
		t.Errorf("file content %q != returned ID %q", string(data), id)
	}

	// File permissions are not meaningful on Windows.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("expected perm 0600, got %04o", perm)
		}
	}
}

func TestLoadOrCreateID_ReusesExistingID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry_id")

	id1, _, err := loadOrCreateID(path)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	id2, created, err := loadOrCreateID(path)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if created {
		t.Error("expected created=false on second call")
	}
	if id1 != id2 {
		t.Errorf("IDs differ: %q != %q", id1, id2)
	}
}

func TestLoadOrCreateID_HandlesCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry_id")

	// Whitespace-only content is treated as empty (corrupt).
	if err := os.WriteFile(path, []byte("   \n"), 0600); err != nil {
		t.Fatal(err)
	}

	id, created, err := loadOrCreateID(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Error("expected created=true when existing file is blank")
	}
	if id == "" {
		t.Fatal("expected non-empty regenerated ID")
	}
	uuidRE := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidRE.MatchString(id) {
		t.Errorf("regenerated ID %q does not match UUID v4 pattern", id)
	}
}

func TestNewUUID_Format(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for i := 0; i < 100; i++ {
		id, err := newUUID()
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if !re.MatchString(id) {
			t.Errorf("iteration %d: %q does not match UUID v4 pattern", i, id)
		}
	}
}

// fixedStats is a StatsProvider that returns constant values for testing.
type fixedStats struct {
	agents, policies int
}

func (s *fixedStats) ConnectedAgentsCount() int { return s.agents }
func (s *fixedStats) ActivePoliciesCount() int  { return s.policies }

// redirectTransport rewrites every outbound request to target, preserving
// the original path so the test server sees the expected URL path.
type redirectTransport struct {
	target *url.URL
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.URL.Scheme = t.target.Scheme
	r.URL.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

func TestPingPayload(t *testing.T) {
	var (
		mu        sync.Mutex
		reqCount  int
		gotMethod string
		gotPath   string
		gotBody   []byte
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		reqCount++
		gotMethod = r.Method
		gotPath = r.URL.Path
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	const testVersion = "0.1.0"
	reporter, _, err := New(testVersion, t.TempDir(), &fixedStats{agents: 3, policies: 5}, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Redirect all pings to the local test server, preserving the request path.
	srvURL, _ := url.Parse(srv.URL)
	reporter.client = &http.Client{Transport: &redirectTransport{target: srvURL}}

	reporter.ping(context.Background())

	mu.Lock()
	defer mu.Unlock()

	if reqCount != 1 {
		t.Fatalf("expected 1 request, got %d", reqCount)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/ping" {
		t.Errorf("path = %q, want /ping", gotPath)
	}

	var payload pingPayload
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("unmarshal body: %v\nbody: %s", err, gotBody)
	}

	uuidRE := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidRE.MatchString(payload.InstanceID) {
		t.Errorf("instance_id %q does not match UUID v4 pattern", payload.InstanceID)
	}
	if payload.Version != testVersion {
		t.Errorf("version = %q, want %q", payload.Version, testVersion)
	}
	if payload.AgentsCount != 3 {
		t.Errorf("agents_count = %d, want 3", payload.AgentsCount)
	}
	if payload.PoliciesCount != 5 {
		t.Errorf("policies_count = %d, want 5", payload.PoliciesCount)
	}
	if payload.OS != runtime.GOOS {
		t.Errorf("os = %q, want %q", payload.OS, runtime.GOOS)
	}
}
