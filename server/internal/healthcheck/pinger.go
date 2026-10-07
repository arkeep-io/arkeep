// Package healthcheck reports backup job lifecycle events to a Healthchecks.io
// (or compatible, self-hosted) check configured on the policy (issue #294).
//
// Push notifications only fire when a job ends; a backup that never starts
// (scheduler stuck, server down, policy disabled) stays silent. A Healthchecks
// check is a dead-man's switch: Arkeep pings it on start, success and failure,
// and Healthchecks alerts when the expected ping does not arrive.
package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

const (
	// maxURLLen bounds the configured ping URL; real ones are well under 200.
	maxURLLen = 2048
	// maxBodyBytes is the Healthchecks limit on a ping body; anything longer
	// is truncated server side anyway.
	maxBodyBytes = 100_000
	// reportTimeout bounds one lifecycle report: job/policy lookup plus the ping.
	reportTimeout = 15 * time.Second
)

// Ping events, appended to the check URL. EventSuccess is the bare URL.
const (
	EventSuccess = ""
	EventStart   = "start"
	EventFail    = "fail"
	EventLog     = "log"
)

// jobReader is the subset of repositories.JobRepository the pinger needs.
type jobReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (*db.Job, error)
	ListDestinationsByJob(ctx context.Context, jobID uuid.UUID) ([]repositories.JobDestinationWithName, error)
}

// policyReader is the subset of repositories.PolicyRepository the pinger needs.
type policyReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (*db.Policy, error)
}

// Pinger sends lifecycle pings for backup jobs whose policy has a
// HealthcheckURL. A nil *Pinger is valid and does nothing.
type Pinger struct {
	client   *http.Client
	jobs     jobReader
	policies policyReader
	logger   *zap.Logger
}

// NewPinger returns a Pinger reading jobs and policies from the given
// repositories.
func NewPinger(jobs jobReader, policies policyReader, logger *zap.Logger) *Pinger {
	return &Pinger{
		client:   &http.Client{Timeout: 10 * time.Second},
		jobs:     jobs,
		policies: policies,
		logger:   logger.Named("healthcheck"),
	}
}

// ValidateURL reports whether raw is usable as a ping URL: an absolute http(s)
// URL with a host.
func ValidateURL(raw string) error {
	if len(raw) > maxURLLen {
		return fmt.Errorf("healthcheck_url must be at most %d characters", maxURLLen)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("healthcheck_url must be a valid http(s) URL")
	}
	return nil
}

// pingURL builds the URL for event on the check at base. Any query already on
// base (e.g. ?create=1 for slug URLs) is kept; rid, when set, is added so
// Healthchecks can pair the start and end of the same run.
func pingURL(base, event, rid string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid ping url: %w", err)
	}
	if event != EventSuccess {
		u = u.JoinPath(event)
	}
	if rid != "" {
		q := u.Query()
		q.Set("rid", rid)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// Ping sends one ping for event to the check at base. body is sent as the
// ping's log text, truncated to the Healthchecks limit. A non-2xx answer is
// an error.
func (p *Pinger) Ping(ctx context.Context, base, event, rid, body string) error {
	target, err := pingURL(base, event, rid)
	if err != nil {
		return err
	}
	if len(body) > maxBodyBytes {
		body = body[:maxBodyBytes]
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to build ping request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("User-Agent", "Arkeep")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // close error is non-actionable
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("ping failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

// ReportJob pings the policy's check for a job status change: "running" pings
// /start, "succeeded" the check itself, "failed" and "cancelled" /fail. msg is
// the job's error or cancel message. Other statuses, non-backup jobs and
// policies without a HealthcheckURL are ignored. Errors are logged, never
// returned: a monitoring ping must not affect the job.
func (p *Pinger) ReportJob(ctx context.Context, jobID uuid.UUID, status, msg string) {
	if p == nil {
		return
	}
	var event string
	switch status {
	case "running":
		event = EventStart
	case "succeeded":
		event = EventSuccess
	case "failed", "cancelled":
		event = EventFail
	default:
		return
	}

	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()

	job, err := p.jobs.GetByID(ctx, jobID)
	if err != nil {
		p.logger.Warn("could not load job", zap.String("job_id", jobID.String()), zap.Error(err))
		return
	}
	// Restores carry the snapshot's policy ID too; only backups are monitored.
	if job.Type != "backup" || job.PolicyID == nil {
		return
	}
	policy, err := p.policies.GetByID(ctx, *job.PolicyID)
	if err != nil {
		p.logger.Warn("could not load policy", zap.String("job_id", jobID.String()), zap.Error(err))
		return
	}
	if policy.HealthcheckURL == "" {
		return
	}

	body := p.jobBody(ctx, job, policy, status, msg)
	if err := p.Ping(ctx, policy.HealthcheckURL, event, jobID.String(), body); err != nil {
		p.logger.Warn("healthcheck ping failed",
			zap.String("job_id", jobID.String()),
			zap.String("policy_id", policy.ID.String()),
			zap.String("status", status),
			zap.Error(err),
		)
	}
}

// jobBody renders the plain-text log attached to a ping.
func (p *Pinger) jobBody(ctx context.Context, job *db.Job, policy *db.Policy, status, msg string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Arkeep backup %s\npolicy: %s\njob: %s\n", status, policy.Name, job.ID)
	if job.StartedAt != nil && job.EndedAt != nil {
		fmt.Fprintf(&b, "duration: %s\n", job.EndedAt.Sub(*job.StartedAt).Round(time.Second))
	}
	if msg != "" {
		fmt.Fprintf(&b, "message: %s\n", msg)
	}
	if status == "running" {
		return b.String()
	}

	dests, err := p.jobs.ListDestinationsByJob(ctx, job.ID)
	if err != nil {
		p.logger.Warn("could not load job destinations", zap.String("job_id", job.ID.String()), zap.Error(err))
		return b.String()
	}
	for _, d := range dests {
		fmt.Fprintf(&b, "destination %s: %s", d.DestinationName, d.Status)
		if d.SnapshotID != "" {
			fmt.Fprintf(&b, ", snapshot %s, %d bytes", d.SnapshotID, d.SizeBytes)
		}
		if d.Error != "" {
			fmt.Fprintf(&b, ", error: %s", d.Error)
		}
		b.WriteString("\n")
	}
	return b.String()
}
