package notification

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

var (
	reportFrom = time.Date(2026, 11, 12, 7, 0, 0, 0, time.UTC)
	reportTo   = reportFrom.Add(24 * time.Hour)
)

// reportJob is a finished job that started at start ("15:04" on Nov 12, or a
// full RFC 3339 time) and ran for d.
func reportJob(jobType, status, start string, d time.Duration) repositories.JobReportRow {
	s, err := time.Parse(time.RFC3339, start)
	if err != nil {
		s, err = time.Parse("2006-01-02 15:04", "2026-11-12 "+start)
		if err != nil {
			panic(err)
		}
	}
	e := s.Add(d)
	return repositories.JobReportRow{Type: jobType, Status: status, StartedAt: &s, EndedAt: &e, AgentName: "srv-01"}
}

func metricLabels(ms []emailMetric) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Label+"="+m.Value)
	}
	return out
}

func TestNewJobReportEmail_Summary(t *testing.T) {
	tests := []struct {
		name         string
		statuses     []string
		wantHeadline string
		wantStripe   string
		wantMetrics  []string
	}{
		{"some failed", []string{"succeeded", "failed", "succeeded"}, "1 of 3 jobs failed", red,
			[]string{"Jobs=3", "Succeeded=2", "Failed=1"}},
		{"all succeeded", []string{"succeeded", "succeeded"}, "All 2 jobs succeeded", greenStripe,
			[]string{"Jobs=2", "Succeeded=2", "Failed=0"}},
		{"one job", []string{"succeeded"}, "1 job succeeded", greenStripe,
			[]string{"Jobs=1", "Succeeded=1", "Failed=0"}},
		{"cancelled and interrupted, none failed", []string{"succeeded", "cancelled", "interrupted"}, "3 jobs finished", amber,
			[]string{"Jobs=3", "Succeeded=1", "Failed=0", "Cancelled=1", "Interrupted=1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var jobs []repositories.JobReportRow
			for _, s := range tt.statuses {
				jobs = append(jobs, reportJob("backup", s, "08:00", time.Minute))
			}
			e := newJobReportEmail(jobs, reportFrom, reportTo, testBaseURL)
			if e.Headline != tt.wantHeadline {
				t.Errorf("Headline = %q, want %q", e.Headline, tt.wantHeadline)
			}
			if e.StatusColor != tt.wantStripe {
				t.Errorf("StatusColor = %q, want %q", e.StatusColor, tt.wantStripe)
			}
			if got := metricLabels(e.Metrics); !reflect.DeepEqual(got, tt.wantMetrics) {
				t.Errorf("Metrics = %v, want %v", got, tt.wantMetrics)
			}
		})
	}
}

func TestNewJobReportEmail_Tables(t *testing.T) {
	backup := reportJob("backup", "succeeded", "08:00", 90*time.Second)
	backup.PolicyName, backup.AddedBytes, backup.SucceededDestinations = "Web root", 48<<20, 2
	failedBackup := reportJob("backup", "failed", "09:00", 34*time.Second)
	failedBackup.PolicyName = "MariaDB dump"
	check := reportJob("check", "succeeded", "10:00", 9*time.Minute)
	check.DestinationName, check.PolicyName = "NAS", "ignored"
	retention := reportJob("retention", "succeeded", "11:00", 17*time.Second)

	// Failed on two of three destinations: the one that succeeded wrote 12 MB.
	partial := reportJob("backup", "failed", "09:30", time.Minute)
	partial.PolicyName, partial.AddedBytes, partial.SucceededDestinations = "Photos", 12<<20, 1
	// Succeeded, but nothing changed since the previous snapshot.
	unchanged := reportJob("backup", "succeeded", "09:45", time.Minute)
	unchanged.PolicyName, unchanged.SucceededDestinations = "Configs", 1

	e := newJobReportEmail([]repositories.JobReportRow{backup, failedBackup, partial, unchanged, check, retention}, reportFrom, reportTo, testBaseURL)

	var titles []string
	for _, tb := range e.Reports {
		titles = append(titles, tb.Title)
	}
	// Tables in a fixed order; job types without jobs have none.
	if want := []string{"Backups", "Retention", "Checks"}; !reflect.DeepEqual(titles, want) {
		t.Fatalf("tables %v, want %v", titles, want)
	}

	backups := e.Reports[0]
	if backups.ExtraLabel != "Added" || e.Reports[1].ExtraLabel != "" || e.Reports[2].ExtraLabel != "" {
		t.Errorf("extra columns %q, %q, %q; want Added for backups only", backups.ExtraLabel, e.Reports[1].ExtraLabel, e.Reports[2].ExtraLabel)
	}
	if got := backups.countsText(); got != "4 jobs · 2 succeeded · 2 failed" {
		t.Errorf("backup counts %q", got)
	}
	want := []reportRow{
		{Name: "Web root", Agent: "srv-01", Started: "08:00", Duration: "1m 30s", Extra: "48.0 MB", Status: "Succeeded", StatusFG: green},
		{Name: "MariaDB dump", Agent: "srv-01", Started: "09:00", Duration: "34s", Extra: "–", Status: "Failed", StatusFG: redFG},
		{Name: "Photos", Agent: "srv-01", Started: "09:30", Duration: "1m 0s", Extra: "12.0 MB", Status: "Failed", StatusFG: redFG},
		{Name: "Configs", Agent: "srv-01", Started: "09:45", Duration: "1m 0s", Extra: "0 B", Status: "Succeeded", StatusFG: green},
	}
	if !reflect.DeepEqual(backups.Rows, want) {
		t.Errorf("backup rows\n got %+v\nwant %+v", backups.Rows, want)
	}
	// Retention and check jobs are named by their destination, not a policy.
	if r := e.Reports[2].Rows[0]; r.Name != "NAS" || r.Agent != "srv-01" {
		t.Errorf("check row %+v, want NAS on srv-01", r)
	}
	if r := e.Reports[1].Rows[0]; r.Name != "Deleted destination" {
		t.Errorf("retention row without destination named %q", r.Name)
	}
	if e.ActionURL != testBaseURL+"/jobs" {
		t.Errorf("ActionURL = %q", e.ActionURL)
	}
	if e.Summary != "Jobs that finished between Nov 12, 2026, 07:00 and Nov 13, 2026, 07:00." {
		t.Errorf("Summary = %q", e.Summary)
	}
	if e.Footer != "Sent by your Arkeep server. Times are in UTC." {
		t.Errorf("Footer = %q", e.Footer)
	}
}

func TestNewJobReportEmail_StartedWithDate(t *testing.T) {
	early := reportJob("backup", "succeeded", "2026-11-12T23:30:00Z", time.Minute)
	late := reportJob("backup", "succeeded", "2026-11-13T01:15:00Z", time.Minute)
	e := newJobReportEmail([]repositories.JobReportRow{early, late}, reportFrom, reportTo, "")
	rows := e.Reports[0].Rows
	if rows[0].Started != "Nov 12, 23:30" || rows[1].Started != "Nov 13, 01:15" {
		t.Errorf("Started %q, %q; want dates when the jobs started on different days", rows[0].Started, rows[1].Started)
	}
	if e.ActionURL != "" {
		t.Errorf("ActionURL = %q without a base URL, want none", e.ActionURL)
	}
}

func TestNewJobReportEmail_RowCap(t *testing.T) {
	var jobs []repositories.JobReportRow
	for i := range maxTableRows + 3 {
		jobs = append(jobs, reportJob("backup", "succeeded", fmt.Sprintf("08:%02d", i%60), time.Second))
	}
	tb := newJobReportEmail(jobs, reportFrom, reportTo, "").Reports[0]
	if len(tb.Rows) != maxTableRows || tb.More != "… and 3 more, see the jobs page." {
		t.Errorf("%d rows, More %q; want %d rows and 3 more", len(tb.Rows), tb.More, maxTableRows)
	}
}

func TestReportRowLimits(t *testing.T) {
	tests := []struct {
		name  string
		sizes []int
		want  []int
	}{
		{"all fit", []int{80, 10, 10, 10}, []int{80, 10, 10, 10}},
		{"one type over its cap", []int{200, 10, 10, 10}, []int{80, 10, 10, 10}},
		{"one table", []int{200}, []int{80}},
		{"small table leaves rows to the large ones", []int{100, 100, 5}, []int{57, 58, 5}},
		{"all full", []int{60, 60, 60, 60}, []int{30, 30, 30, 30}},
		{"below every cap", []int{3, 1}, []int{3, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reportRowLimits(tt.sizes)
			if !slices.Equal(got, tt.want) {
				t.Errorf("reportRowLimits(%v) = %v, want %v", tt.sizes, got, tt.want)
			}
			total := 0
			for _, n := range got {
				total += n
			}
			if total > maxReportRows {
				t.Errorf("%d rows in total, want at most %d", total, maxReportRows)
			}
		})
	}
}

func TestRenderJobReport(t *testing.T) {
	failed := reportJob("backup", "failed", "08:00", time.Minute)
	failed.PolicyName = "<Photos>"
	ok := reportJob("check", "succeeded", "09:00", time.Minute)
	ok.DestinationName = "NAS"
	text, html, err := renderEmailBodies(newJobReportEmail([]repositories.JobReportRow{failed, ok}, reportFrom, reportTo, testBaseURL))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"1 of 2 jobs failed", "Backups", "Checks", "&lt;Photos&gt;", ">Added<", "color:#ff8a90;\">Failed<", `href="https://arkeep.example.com/jobs"`} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML is missing %q", want)
		}
	}
	for _, want := range []string{"1 of 2 jobs failed\n", "Backups (1 job · 1 failed):\n- <Photos> · srv-01: Failed\n  08:00 · 1m 0s\n", "Checks (1 job · 1 succeeded):\n- NAS · srv-01: Succeeded\n", "Open jobs: https://arkeep.example.com/jobs"} {
		if !strings.Contains(text, want) {
			t.Errorf("text is missing %q:\n%s", want, text)
		}
	}
}

func TestSendJobReport(t *testing.T) {
	ctx := context.Background()

	setup := func(t *testing.T, smtp bool) (*NotificationService, repositories.SettingsRepository, <-chan []byte) {
		t.Helper()
		svc, gdb := newTestService(t)
		svc.baseURL = testBaseURL
		settings := repositories.NewSettingsRepository(gdb)
		var received <-chan []byte
		if smtp {
			var port int
			port, received = startFakeSMTP(t)
			for k, v := range map[string]string{KeySMTPHost: "127.0.0.1", KeySMTPPort: strconv.Itoa(port), KeySMTPFrom: "noreply@test.local"} {
				if err := settings.Set(ctx, k, db.EncryptedString(v)); err != nil {
					t.Fatalf("settings.Set(%s): %v", k, err)
				}
			}
		}
		admin := &db.User{Email: "admin@test.local", DisplayName: "Admin", Role: "admin", IsActive: true}
		if err := repositories.NewUserRepository(gdb).Create(ctx, admin); err != nil {
			t.Fatalf("create admin: %v", err)
		}
		seedJob(t, gdb, "boom") // a failed backup that ended 2026-11-05T02:04:37Z
		return svc, settings, received
	}
	from, to := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)

	t.Run("sends the report", func(t *testing.T) {
		svc, _, received := setup(t, true)
		outcome, err := svc.SendJobReport(ctx, from, to)
		if err != nil || outcome != ReportSent {
			t.Fatalf("SendJobReport = %v, %v; want sent", outcome, err)
		}
		select {
		case msg := <-received:
			parts := readEmailParts(t, msg)
			if len(parts) != 3 || !strings.Contains(parts[1].body, "1 of 1 job failed") {
				t.Errorf("report email parts %+v, want text, HTML with the headline and logo", parts)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no message reached the SMTP server")
		}
	})

	t.Run("nothing to send when no job ended", func(t *testing.T) {
		svc, _, _ := setup(t, true)
		if outcome, err := svc.SendJobReport(ctx, to, to.Add(time.Hour)); err != nil || outcome != ReportEmpty {
			t.Errorf("SendJobReport = %v, %v; want empty", outcome, err)
		}
	})

	t.Run("skipped without SMTP", func(t *testing.T) {
		svc, _, _ := setup(t, false)
		if outcome, err := svc.SendJobReport(ctx, from, to); err != nil || outcome != ReportSkipped {
			t.Errorf("SendJobReport = %v, %v; want skipped", outcome, err)
		}
	})

	t.Run("sent while email notifications are off", func(t *testing.T) {
		svc, settings, received := setup(t, true)
		if err := settings.Set(ctx, KeyEmailEnabled, "false"); err != nil {
			t.Fatal(err)
		}
		if outcome, err := svc.SendJobReport(ctx, from, to); err != nil || outcome != ReportSent {
			t.Fatalf("SendJobReport = %v, %v; want sent", outcome, err)
		}
		select {
		case <-received:
		case <-time.After(5 * time.Second):
			t.Fatal("no message reached the SMTP server")
		}
	})
}
