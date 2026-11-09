package notification

import (
	"context"
	"encoding/json"
	"net"
	"net/textproto"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Vienna and Asia/Dubai without system zone data

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

const testBaseURL = "https://arkeep.example.com"

// TestMain fixes the email's time zone to UTC, so the expected times do not
// depend on the machine running the tests.
func TestMain(m *testing.M) {
	emailLocation = time.UTC
	os.Exit(m.Run())
}

func at(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

// gb12 is 1.2 GB as formatEmailBytes prints it.
const gb12 = 1288490188

// The example jobs of the design preview: a backup to two destinations that
// succeeded, and one where the first destination failed.
var (
	successJob = jobEmailData{
		JobID:      "3f0c8a52-6d1e-4b7a-9c44-1e2f7ab90d13",
		JobType:    "backup",
		PolicyName: "Daily server backup",
		AgentName:  "web-01",
		StartedAt:  at("2026-11-05T02:00:00Z"),
		EndedAt:    at("2026-11-05T02:12:04Z"),
		Destinations: []destinationResult{
			{Name: "offsite-s3", Status: "succeeded", SnapshotIDs: []string{"8f3a1c7e5b2d4f60a1b2c3d4e5f60718293a4b5c6d7e8f9012345678abcdef01"}, AddedBytes: gb12, StartedAt: at("2026-11-05T02:00:01Z"), EndedAt: at("2026-11-05T02:08:42Z")},
			{Name: "local-nas", Status: "succeeded", SnapshotIDs: []string{"c20b94d1aa"}, AddedBytes: gb12, StartedAt: at("2026-11-05T02:08:42Z"), EndedAt: at("2026-11-05T02:12:05Z")},
		},
	}
	failureJob = jobEmailData{
		JobID:      "b81d47e0-2c3a-49f6-8e15-7a60cd13f2e8",
		JobType:    "backup",
		PolicyName: "Daily server backup",
		AgentName:  "web-01",
		StartedAt:  at("2026-11-05T02:00:00Z"),
		EndedAt:    at("2026-11-05T02:04:37Z"),
		Destinations: []destinationResult{
			{Name: "offsite-s3", Status: "failed", StartedAt: at("2026-11-05T02:00:01Z"), EndedAt: at("2026-11-05T02:01:15Z")},
			{Name: "local-nas", Status: "succeeded", SnapshotIDs: []string{"c20b94d1aa"}, AddedBytes: gb12, StartedAt: at("2026-11-05T02:01:14Z"), EndedAt: at("2026-11-05T02:04:37Z")},
		},
		Error: failureError,
	}
	failureError = "Fatal: unable to open repository at s3:s3.eu-central-1.amazonaws.com/acme-backups: 403 Forbidden"
)

// The emails of the design preview, as job.html was designed against them.
var (
	wantSuccess = jobEmail{
		Title:       "Backup completed: Daily server backup",
		Preheader:   "Daily server backup finished in 12m 4s. 2.4 GB added, 2 of 2 destinations succeeded.",
		StatusLabel: "Succeeded", StatusColor: greenStripe, StatusBG: greenBG, StatusFG: green,
		Headline: "Backup completed",
		Summary:  "Daily server backup on web-01",
		Metrics: []emailItem{
			{"Duration", "12m 4s"},
			{"Added", "2.4 GB"},
			{"Destinations", "2 of 2"},
		},
		Destinations: []destinationRow{
			{Name: "offsite-s3", Status: "Succeeded", StatusFG: green, Meta: "Snapshot 8f3a1c7e · 1.2 GB added · 8m 41s"},
			{Name: "local-nas", Status: "Succeeded", StatusFG: green, Meta: "Snapshot c20b94d1 · 1.2 GB added · 3m 23s"},
		},
		Details: []emailItem{
			{"Started", "Nov 5, 2026, 02:00"},
			{"Finished", "Nov 5, 2026, 02:12"},
			{"Job ID", "3f0c8a52-6d1e-4b7a-9c44-1e2f7ab90d13"},
		},
		ActionURL: "https://arkeep.example.com/jobs/3f0c8a52-6d1e-4b7a-9c44-1e2f7ab90d13", ActionLabel: "View job",
		Footer: "Sent by your Arkeep server. Times are in UTC.",
	}
	wantFailure = jobEmail{
		Title:       "Backup failed: Daily server backup",
		Preheader:   "Daily server backup failed after 4m 37s. 1 of 2 destinations succeeded.",
		StatusLabel: "Failed", StatusColor: red, StatusBG: redBG, StatusFG: redFG,
		Headline: "Backup failed",
		Summary:  "Daily server backup on web-01",
		Metrics: []emailItem{
			{"Duration", "4m 37s"},
			{"Added", "1.2 GB"},
			{"Destinations", "1 of 2"},
		},
		ErrorTitle: "Error",
		Error:      failureError,
		Destinations: []destinationRow{
			{Name: "offsite-s3", Status: "Failed", StatusFG: redFG, Meta: "No snapshot created · 1m 14s"},
			{Name: "local-nas", Status: "Succeeded", StatusFG: green, Meta: "Snapshot c20b94d1 · 1.2 GB added · 3m 23s"},
		},
		Details: []emailItem{
			{"Started", "Nov 5, 2026, 02:00"},
			{"Finished", "Nov 5, 2026, 02:04"},
			{"Job ID", "b81d47e0-2c3a-49f6-8e15-7a60cd13f2e8"},
		},
		ActionURL: "https://arkeep.example.com/jobs/b81d47e0-2c3a-49f6-8e15-7a60cd13f2e8", ActionLabel: "View job",
		Footer: "Sent by your Arkeep server. Times are in UTC.",
	}
)

// emailFor builds the email of a job with the title the service gives it.
func emailFor(data jobEmailData, failed bool, baseURL string) jobEmail {
	operation, name, _ := JobSubject{Type: data.JobType, PolicyName: data.PolicyName, DestinationName: data.DestinationName}.describe()
	title := operation + " completed: " + name
	if failed {
		title = operation + " failed: " + name
	}
	notifAt := time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC)
	return newJobEmail(title, failed, data, notifAt, baseURL)
}

func TestNewJobEmail_DesignExamples(t *testing.T) {
	if got := emailFor(successJob, false, testBaseURL); !reflect.DeepEqual(got, wantSuccess) {
		t.Errorf("success email:\n got %+v\nwant %+v", got, wantSuccess)
	}
	if got := emailFor(failureJob, true, testBaseURL); !reflect.DeepEqual(got, wantFailure) {
		t.Errorf("failure email:\n got %+v\nwant %+v", got, wantFailure)
	}
}

// TestNewJobEmail_JobTypes renders every job type for success and failure and
// checks what each one shows.
func TestNewJobEmail_JobTypes(t *testing.T) {
	base := jobEmailData{
		JobID:     "3f0c8a52-6d1e-4b7a-9c44-1e2f7ab90d13",
		AgentName: "web-01",
		StartedAt: at("2026-11-05T02:00:00Z"),
		EndedAt:   at("2026-11-05T02:00:45Z"),
	}
	oneDest := []destinationResult{{Name: "nas", Status: "succeeded"}}

	tests := []struct {
		name         string
		job          func(jobEmailData) jobEmailData
		wantHeadline string
		wantSummary  string
		wantMetrics  []string
		wantDestList bool
	}{
		{"backup", func(j jobEmailData) jobEmailData {
			j.JobType, j.PolicyName, j.Destinations = "backup", "nightly", oneDest
			return j
		}, "Backup", "nightly on web-01", []string{"Duration", "Added", "Destinations"}, true},
		{"restore", func(j jobEmailData) jobEmailData {
			j.JobType, j.PolicyName, j.DestinationName, j.Destinations = "restore", "nightly", "nas", oneDest
			return j
		}, "Restore", "nightly on web-01", []string{"Duration"}, false},
		{"retention", func(j jobEmailData) jobEmailData {
			j.JobType, j.DestinationName, j.Destinations = "retention", "nas", oneDest
			return j
		}, "Retention", "nas by web-01", []string{"Duration"}, false},
		{"check", func(j jobEmailData) jobEmailData {
			j.JobType, j.DestinationName, j.Destinations = "check", "nas", oneDest
			return j
		}, "Integrity check", "nas by web-01", []string{"Duration"}, false},
	}
	for _, tt := range tests {
		for _, failed := range []bool{false, true} {
			outcome := " completed"
			if failed {
				outcome = " failed"
			}
			t.Run(tt.name+outcome, func(t *testing.T) {
				data := tt.job(base)
				if failed {
					data.Error = "boom"
				}
				e := emailFor(data, failed, testBaseURL)
				if e.Headline != tt.wantHeadline+outcome {
					t.Errorf("Headline = %q, want %q", e.Headline, tt.wantHeadline+outcome)
				}
				if e.Summary != tt.wantSummary {
					t.Errorf("Summary = %q, want %q", e.Summary, tt.wantSummary)
				}
				var labels []string
				for _, m := range e.Metrics {
					labels = append(labels, m.Label)
				}
				if !reflect.DeepEqual(labels, tt.wantMetrics) {
					t.Errorf("Metrics = %v, want %v", labels, tt.wantMetrics)
				}
				if got := len(e.Destinations) > 0; got != tt.wantDestList {
					t.Errorf("destination list shown = %v, want %v", got, tt.wantDestList)
				}
				if (e.Error != "") != failed {
					t.Errorf("Error = %q, want an error block only on failure", e.Error)
				}

				text, html, err := jobEmailBodies(failed, e.Title, data, time.Now(), testBaseURL)
				if err != nil {
					t.Fatalf("jobEmailBodies: %v", err)
				}
				for _, want := range []string{e.Headline + "\n" + tt.wantSummary + "\n", "View job: https://arkeep.example.com/jobs/"} {
					if !strings.Contains(text, want) {
						t.Errorf("text is missing %q:\n%s", want, text)
					}
				}
				for _, want := range []string{e.Headline, `src="cid:arkeep-logo"`, `href="https://arkeep.example.com/jobs/3f0c8a52-6d1e-4b7a-9c44-1e2f7ab90d13"`, "<!--[if mso]>"} {
					if !strings.Contains(html, want) {
						t.Errorf("HTML is missing %q", want)
					}
				}
			})
		}
	}
}

// TestRenderJobEmailText pins the plain-text version of the failure email of
// the design preview, the part mail clients without HTML show.
func TestRenderJobEmailText(t *testing.T) {
	e := wantFailure
	e.Error = "first line\nsecond line"
	e.Destinations[0].Error = "403 Forbidden\nbucket acme-backups"
	want := `Backup failed
Daily server backup on web-01

Duration:      4m 37s
Added:         1.2 GB
Destinations:  1 of 2

Error:
  first line
  second line

Destinations:
- offsite-s3: Failed
  No snapshot created · 1m 14s
    403 Forbidden
    bucket acme-backups
- local-nas: Succeeded
  Snapshot c20b94d1 · 1.2 GB added · 3m 23s

Started:   Nov 5, 2026, 02:00
Finished:  Nov 5, 2026, 02:04
Job ID:    b81d47e0-2c3a-49f6-8e15-7a60cd13f2e8

View job: https://arkeep.example.com/jobs/b81d47e0-2c3a-49f6-8e15-7a60cd13f2e8

-- 
Sent by your Arkeep server. Times are in UTC.
`
	if got := renderJobEmailText(e); got != want {
		t.Errorf("text email:\n%s\nwant:\n%s", got, want)
	}
}

// TestJobEmail_Escaping checks that names and error messages cannot inject
// markup, and that the line breaks of an error survive into the email.
func TestJobEmail_Escaping(t *testing.T) {
	job := failureJob
	job.PolicyName = `<script>alert("policy")</script>`
	job.AgentName = `<img src=x onerror=alert(1)>`
	job.Destinations = []destinationResult{{Name: `<b>dest</b>`, Status: "failed", Error: "<i>dest error</i>"}}
	job.Error = "line one <b>bold</b>\nline two"

	_, html, err := jobEmailBodies(true, "Backup failed: "+job.PolicyName, job, time.Now(), testBaseURL)
	if err != nil {
		t.Fatalf("jobEmailBodies: %v", err)
	}
	for _, bad := range []string{"<script>", "<img src=x", "<b>", "<i>"} {
		if strings.Contains(html, bad) {
			t.Errorf("HTML contains unescaped %q", bad)
		}
	}
	for _, want := range []string{"&lt;script&gt;", "&lt;b&gt;dest&lt;/b&gt;", "&lt;i&gt;dest error&lt;/i&gt;", "line one &lt;b&gt;bold&lt;/b&gt;\nline two"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML is missing escaped %q", want)
		}
	}
}

// TestJobEmail_MissingData covers a failure from the scheduler (no
// destinations, no start time), a server without base URL, and an email
// rendered without logo.
func TestJobEmail_MissingData(t *testing.T) {
	job := jobEmailData{JobID: "b81d47e0-2c3a-49f6-8e15-7a60cd13f2e8", JobType: "backup", PolicyName: "nightly", Error: "all destinations unavailable: backup not run"}
	e := emailFor(job, true, "")

	if len(e.Metrics) != 0 || len(e.Destinations) != 0 {
		t.Errorf("Metrics %v, Destinations %v; want neither without job details", e.Metrics, e.Destinations)
	}
	if e.Summary != "nightly" || e.Preheader != "nightly failed." {
		t.Errorf("Summary %q, Preheader %q; want the policy name alone", e.Summary, e.Preheader)
	}
	// With no end time the notification's creation time stands in.
	if want := []emailItem{{"Finished", "Nov 5, 2026, 03:00"}, {"Job ID", "b81d47e0-2c3a-49f6-8e15-7a60cd13f2e8"}}; !reflect.DeepEqual(e.Details, want) {
		t.Errorf("Details = %v, want %v", e.Details, want)
	}
	if e.ActionURL != "" {
		t.Errorf("ActionURL = %q, want none without a base URL", e.ActionURL)
	}

	html, err := renderJobEmail(e)
	if err != nil {
		t.Fatalf("renderJobEmail: %v", err)
	}
	for _, bad := range []string{"<img", "View job", "Key figures", "Destinations</div>"} {
		if strings.Contains(html, bad) {
			t.Errorf("HTML contains %q, want it left out", bad)
		}
	}
	if !strings.Contains(html, "all destinations unavailable") {
		t.Error("HTML is missing the error")
	}
}

// TestFormatEmailTime checks that times follow the server's zone (TZ), and
// that the footer names it by its abbreviation or UTC offset, never by the
// IANA name.
func TestFormatEmailTime(t *testing.T) {
	defer func(l *time.Location) { emailLocation = l }(emailLocation)
	load := func(name string) *time.Location {
		l, err := time.LoadLocation(name)
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		return l
	}
	winter := time.Date(2026, 11, 5, 2, 0, 0, 0, time.UTC)
	summer := time.Date(2026, 7, 5, 2, 0, 0, 0, time.UTC)
	tests := []struct {
		loc      *time.Location
		at       time.Time
		wantTime string
		wantZone string
	}{
		{time.UTC, winter, "Nov 5, 2026, 02:00", "UTC"},
		{load("Europe/Vienna"), winter, "Nov 5, 2026, 03:00", "CET"},
		{load("Europe/Vienna"), summer, "Jul 5, 2026, 04:00", "CEST"},
		{load("Asia/Dubai"), winter, "Nov 5, 2026, 06:00", "UTC+4"},
		{load("Asia/Kolkata"), winter, "Nov 5, 2026, 07:30", "IST"},
		{time.FixedZone("", -(3*3600 + 30*60)), winter, "Nov 4, 2026, 22:30", "UTC-3:30"},
	}
	for _, tt := range tests {
		emailLocation = tt.loc
		if got := formatEmailTime(tt.at); got != tt.wantTime {
			t.Errorf("formatEmailTime in %s = %q, want %q", tt.loc, got, tt.wantTime)
		}
		if got := zoneLabel(tt.at.In(tt.loc)); got != tt.wantZone {
			t.Errorf("zoneLabel in %s = %q, want %q", tt.loc, got, tt.wantZone)
		}
	}

	// The footer names the zone of the job's end.
	emailLocation = load("Europe/Vienna")
	e := emailFor(successJob, false, "")
	if want := "Sent by your Arkeep server. Times are in CET."; e.Footer != want {
		t.Errorf("Footer = %q, want %q", e.Footer, want)
	}

	// A job across the end of daylight saving time (Oct 25, 2026, 03:00 CEST
	// becomes 02:00 CET) names the zone at each time instead.
	job := successJob
	job.StartedAt, job.EndedAt = at("2026-10-25T00:30:00Z"), at("2026-10-25T01:10:00Z")
	e = emailFor(job, false, "")
	if want := "Sent by your Arkeep server."; e.Footer != want {
		t.Errorf("Footer across the change = %q, want %q", e.Footer, want)
	}
	if want := []emailItem{{"Started", "Oct 25, 2026, 02:30 CEST"}, {"Finished", "Oct 25, 2026, 02:10 CET"}}; !reflect.DeepEqual(e.Details[:2], want) {
		t.Errorf("Details across the change = %v, want %v", e.Details[:2], want)
	}
}

func TestJobEmailDataFrom(t *testing.T) {
	dest := uuid.Must(uuid.NewV7())
	other := uuid.Must(uuid.NewV7())
	job := &repositories.JobWithNames{
		Job:        db.Job{Type: "backup", Error: "first line\nsecond line"},
		PolicyName: "nightly",
		AgentName:  "web-01",
	}
	dests := []repositories.JobDestinationWithName{{
		JobDestination:  db.JobDestination{DestinationID: dest, Status: "succeeded", SnapshotID: "aaaa1111", SizeBytes: 100},
		DestinationName: "nas",
	}}
	// A command source is a snapshot of its own, on the destination it ran for.
	cmds := []repositories.JobDestinationCommandWithName{
		{JobDestinationCommand: db.JobDestinationCommand{DestinationID: dest, SnapshotID: "bbbb2222", SizeBytes: 50}},
		{JobDestinationCommand: db.JobDestinationCommand{DestinationID: other, SnapshotID: "cccc3333", SizeBytes: 999}},
	}
	got := jobEmailDataFrom(job, dests, cmds)
	if got.PolicyName != "nightly" || got.AgentName != "web-01" || got.DestinationName != "nas" {
		t.Errorf("names = %q, %q, %q; want nightly, web-01, nas", got.PolicyName, got.AgentName, got.DestinationName)
	}
	if got.Error != "first line\nsecond line" {
		t.Errorf("Error = %q, want the job error with its line break", got.Error)
	}
	want := destinationResult{Name: "nas", Status: "succeeded", SnapshotIDs: []string{"aaaa1111", "bbbb2222"}, AddedBytes: 150}
	if len(got.Destinations) != 1 || !reflect.DeepEqual(got.Destinations[0], want) {
		t.Errorf("Destinations = %+v, want [%+v]", got.Destinations, want)
	}
}

// TestJobSubjectPayload checks that the payload, which also goes to webhooks,
// carries no job details for the email.
func TestJobSubjectPayload(t *testing.T) {
	var keys []string
	for k := range (JobSubject{Type: "backup"}).payload() {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"destination_name", "job_id", "job_type", "policy_id", "policy_name"}; !slices.Equal(keys, want) {
		t.Errorf("payload keys = %v, want %v", keys, want)
	}
}

// seedJob stores a failed backup to one destination, as the email loads it.
func seedJob(t *testing.T, gdb *gorm.DB, jobErr string) *db.Job {
	t.Helper()
	ctx := context.Background()
	agent := &db.Agent{Name: "web-01", Status: "online"}
	if err := repositories.NewAgentRepository(gdb).Create(ctx, agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	dest := &db.Destination{Name: "offsite-s3", Type: "local", Config: "{}", Enabled: true}
	if err := repositories.NewDestinationRepository(gdb).Create(ctx, dest); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	jobs := repositories.NewJobRepository(gdb)
	job := &db.Job{AgentID: agent.ID, Type: "backup", Status: "failed", StartedAt: at("2026-11-05T02:00:00Z"), EndedAt: at("2026-11-05T02:04:37Z"), Error: jobErr}
	if err := jobs.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	jd := &db.JobDestination{JobID: job.ID, DestinationID: dest.ID, Status: "failed", StartedAt: at("2026-11-05T02:00:01Z"), EndedAt: at("2026-11-05T02:01:15Z")}
	if err := jobs.CreateDestination(ctx, jd); err != nil {
		t.Fatalf("create job destination: %v", err)
	}
	return job
}

// TestEmailBodies_PlainText covers the emails that stay short plain text:
// those not about a job, and those whose job can no longer be loaded.
func TestEmailBodies_PlainText(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	tests := []struct{ name, typ, payload string }{
		{"agent event", "agent_offline", `{"agent_name":"web-01"}`},
		{"job deleted", "job_failure", `{"job_id":"` + uuid.Must(uuid.NewV7()).String() + `","error":"boom"}`},
		{"no job id", "job_success", `{}`},
	}
	for _, tt := range tests {
		n := &db.Notification{Type: tt.typ, Title: "t", Body: "b", Payload: tt.payload}
		if text, html := svc.emailBodies(ctx, n); text != "b" || html != "" {
			t.Errorf("%s: got text %q and HTML %v, want the notification body alone", tt.name, text, html != "")
		}
	}
}

// TestRetryDelivery_HTMLEmail checks that a retried job email is the HTML
// email again, with the job details loaded from the database.
func TestRetryDelivery_HTMLEmail(t *testing.T) {
	ctx := context.Background()
	svc, gdb := newTestService(t)
	svc.baseURL = testBaseURL
	settings := repositories.NewSettingsRepository(gdb)
	notifs := repositories.NewNotificationRepository(gdb)

	port, received := startFakeSMTP(t)
	for k, v := range map[string]string{KeySMTPHost: "127.0.0.1", KeySMTPPort: strconv.Itoa(port), KeySMTPFrom: "noreply@test.local"} {
		if err := settings.Set(ctx, k, db.EncryptedString(v)); err != nil {
			t.Fatalf("settings.Set(%s): %v", k, err)
		}
	}
	admin := &db.User{Email: "admin@test.local", DisplayName: "Admin", Role: "admin", IsActive: true}
	if err := repositories.NewUserRepository(gdb).Create(ctx, admin); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	job := seedJob(t, gdb, "first line\nsecond line")
	payload := JobSubject{JobID: job.ID, Type: "backup", PolicyName: "Daily server backup"}.payload()
	payload["error"] = sanitizeHeader(job.Error)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	n := &db.Notification{UserID: admin.ID, Type: "job_failure", Title: "Backup failed: Daily server backup", Body: "text body", Payload: string(raw)}
	if err := notifs.Create(ctx, n); err != nil {
		t.Fatalf("create notification: %v", err)
	}
	d := &db.NotificationDelivery{NotificationID: n.ID, Type: "email", Status: "pending"}
	if err := notifs.CreateDelivery(ctx, d); err != nil {
		t.Fatalf("create delivery: %v", err)
	}

	svc.retryDelivery(ctx, d)
	if d.Status != "sent" {
		t.Fatalf("delivery status %q (%s), want sent", d.Status, d.LastError)
	}
	var msg []byte
	select {
	case msg = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("no message reached the SMTP server")
	}
	parts := readEmailParts(t, msg)
	if len(parts) != 3 || parts[0].contentType != "text/plain" || parts[1].contentType != "text/html" {
		t.Fatalf("retried email has parts %+v, want text, HTML and logo", parts)
	}
	for _, want := range []string{"Backup failed", "web-01", "first line\nsecond line", "offsite-s3", "4m 37s", `href="https://arkeep.example.com/jobs/` + job.ID.String() + `"`} {
		if !strings.Contains(parts[1].body, want) {
			t.Errorf("retried HTML is missing %q", want)
		}
	}
	text := strings.ReplaceAll(parts[0].body, "\r\n", "\n")
	for _, want := range []string{"Backup failed\n", "web-01", "  first line\n  second line\n", "- offsite-s3: Failed", "Duration:      4m 37s", "View job: https://arkeep.example.com/jobs/" + job.ID.String()} {
		if !strings.Contains(text, want) {
			t.Errorf("retried text is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "text body") {
		t.Error("retried text is the short notification body, want the full job email")
	}
}

// startFakeSMTP accepts SMTP sessions on a local port, without TLS or AUTH,
// and passes every message it receives to the returned channel.
func startFakeSMTP(t *testing.T) (int, <-chan []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	received := make(chan []byte, 4)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeSMTP(conn, received)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, received
}

func serveFakeSMTP(conn net.Conn, received chan<- []byte) {
	defer func() { _ = conn.Close() }()
	tp := textproto.NewConn(conn)
	reply := func(s string) { _ = tp.PrintfLine("%s", s) }
	reply("220 fake ESMTP")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		switch cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0]); cmd {
		case "EHLO", "HELO", "MAIL", "RCPT", "RSET", "NOOP":
			reply("250 ok")
		case "DATA":
			reply("354 go ahead")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			received <- data
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 not implemented")
		}
	}
}
