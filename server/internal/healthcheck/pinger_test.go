package healthcheck

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

type fakeJobs struct {
	job   *db.Job
	dests []repositories.JobDestinationWithName
}

func (f *fakeJobs) GetByID(_ context.Context, _ uuid.UUID) (*db.Job, error) { return f.job, nil }
func (f *fakeJobs) ListDestinationsByJob(_ context.Context, _ uuid.UUID) ([]repositories.JobDestinationWithName, error) {
	return f.dests, nil
}

type fakePolicies struct{ policy *db.Policy }

func (f *fakePolicies) GetByID(_ context.Context, _ uuid.UUID) (*db.Policy, error) {
	return f.policy, nil
}

// pingRecorder is an httptest server recording each request's path, query
// and body.
type pingRecorder struct {
	mu     sync.Mutex
	pings  []recordedPing
	status int
	srv    *httptest.Server
}

type recordedPing struct {
	Path, RawQuery, Body string
}

func newPingRecorder(t *testing.T) *pingRecorder {
	t.Helper()
	rec := &pingRecorder{status: http.StatusOK}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.pings = append(rec.pings, recordedPing{r.URL.Path, r.URL.RawQuery, string(body)})
		status := rec.status
		rec.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

func (r *pingRecorder) all() []recordedPing {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedPing(nil), r.pings...)
}

func TestPingURL(t *testing.T) {
	tests := []struct {
		name, base, event, rid, want string
	}{
		{"success", "https://hc-ping.com/abc", EventSuccess, "r1", "https://hc-ping.com/abc?rid=r1"},
		{"start", "https://hc-ping.com/abc", EventStart, "r1", "https://hc-ping.com/abc/start?rid=r1"},
		{"fail trailing slash", "https://hc-ping.com/abc/", EventFail, "r1", "https://hc-ping.com/abc/fail?rid=r1"},
		{"query kept", "https://hc.example.com/ping/key/my-slug?create=1", EventFail, "r1", "https://hc.example.com/ping/key/my-slug/fail?create=1&rid=r1"},
		{"no rid", "http://10.0.0.5:8000/ping/abc", EventLog, "", "http://10.0.0.5:8000/ping/abc/log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pingURL(tt.base, tt.event, tt.rid)
			if err != nil {
				t.Fatalf("pingURL: %v", err)
			}
			if got != tt.want {
				t.Errorf("pingURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"hc-ping", "https://hc-ping.com/5f0c9f9e-1f1e-4e5b-9c7a-1b2c3d4e5f60", false},
		{"self-hosted http", "http://healthchecks.lan:8000/ping/abc", false},
		{"no scheme", "hc-ping.com/abc", true},
		{"ftp", "ftp://hc-ping.com/abc", true},
		{"no host", "https:///abc", true},
		{"too long", "https://hc-ping.com/" + strings.Repeat("a", maxURLLen), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateURL(tt.url); (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestPing_TruncatesBodyAndReportsHTTPErrors(t *testing.T) {
	rec := newPingRecorder(t)
	p := NewPinger(&fakeJobs{}, &fakePolicies{}, zap.NewNop())

	if err := p.Ping(context.Background(), rec.srv.URL+"/abc", EventLog, "", strings.Repeat("x", maxBodyBytes+10)); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got := len(rec.all()[0].Body); got != maxBodyBytes {
		t.Errorf("body length = %d, want %d", got, maxBodyBytes)
	}

	rec.mu.Lock()
	rec.status = http.StatusNotFound
	rec.mu.Unlock()
	err := p.Ping(context.Background(), rec.srv.URL+"/abc", EventLog, "", "x")
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("Ping on 404 error = %v, want HTTP 404", err)
	}
}

func TestReportJob(t *testing.T) {
	policyID := uuid.New()
	jobID := uuid.New()

	tests := []struct {
		name       string
		jobType    string
		policyID   *uuid.UUID
		url        bool
		status     string
		wantPath   string // "" = no ping expected
		wantInBody string
	}{
		{"running pings start", "backup", &policyID, true, "running", "/abc/start", "policy: nightly"},
		{"succeeded pings base", "backup", &policyID, true, "succeeded", "/abc", "snapshot snap1, 2048 bytes"},
		{"failed pings fail", "backup", &policyID, true, "failed", "/abc/fail", "message: boom"},
		{"cancelled pings fail", "backup", &policyID, true, "cancelled", "/abc/fail", "Arkeep backup cancelled"},
		{"interrupted ignored", "backup", &policyID, true, "interrupted", "", ""},
		{"restore ignored", "restore", &policyID, true, "succeeded", "", ""},
		{"no policy ignored", "backup", nil, true, "succeeded", "", ""},
		{"no url ignored", "backup", &policyID, false, "succeeded", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newPingRecorder(t)
			policy := &db.Policy{Name: "nightly"}
			policy.ID = policyID
			if tt.url {
				policy.HealthcheckURL = rec.srv.URL + "/abc"
			}
			job := &db.Job{Type: tt.jobType, PolicyID: tt.policyID}
			job.ID = jobID
			jobs := &fakeJobs{job: job, dests: []repositories.JobDestinationWithName{{
				JobDestination:  db.JobDestination{Status: "succeeded", SnapshotID: "snap1", SizeBytes: 2048},
				DestinationName: "s3",
			}}}
			p := NewPinger(jobs, &fakePolicies{policy: policy}, zap.NewNop())

			p.ReportJob(context.Background(), jobID, tt.status, "boom")

			pings := rec.all()
			if tt.wantPath == "" {
				if len(pings) != 0 {
					t.Fatalf("got %d pings, want none", len(pings))
				}
				return
			}
			if len(pings) != 1 {
				t.Fatalf("got %d pings, want 1", len(pings))
			}
			if pings[0].Path != tt.wantPath {
				t.Errorf("path = %q, want %q", pings[0].Path, tt.wantPath)
			}
			if pings[0].RawQuery != "rid="+jobID.String() {
				t.Errorf("query = %q, want rid=%s", pings[0].RawQuery, jobID)
			}
			if !strings.Contains(pings[0].Body, tt.wantInBody) {
				t.Errorf("body %q does not contain %q", pings[0].Body, tt.wantInBody)
			}
		})
	}
}

func TestReportJob_NilPinger(t *testing.T) {
	var p *Pinger
	p.ReportJob(context.Background(), uuid.New(), "succeeded", "") // must not panic
}
