package notification

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// The job report: one email that lists every job that finished in a period,
// sent on a schedule (see package jobreport) instead of, or next to, one email
// per job.

// The rows of the job report are capped; the rest are counted below each
// table, and the full list is in the GUI. maxReportRows keeps the email below
// the size at which Gmail clips it (measured: about 140 rows), maxTableRows
// keeps one job type from filling the whole email.
const (
	maxReportRows = 120
	maxTableRows  = 80
)

// reportTable is the table of one job type in the job report.
type reportTable struct {
	Title string
	// Counts summarise the table ("6 jobs", "4 succeeded", "2 failed").
	Counts []reportCount
	// ExtraLabel is the header of the column only this job type has (e.g.
	// "Added" for backups). Empty leaves the column out.
	ExtraLabel string
	// StartedWidth is the width in pixels of the Started column: wider when
	// the times carry a date.
	StartedWidth int
	Rows         []reportRow
	// More is the line below the table when rows were left out.
	More string
}

// reportRow is one job in a table of the job report.
type reportRow struct {
	Name, Agent       string
	Started, Duration string
	Extra             string
	Status, StatusFG  string
}

// reportCount is one part of the summary line of a table. Color is set on
// counts that need attention; empty is the muted text color.
type reportCount struct{ Text, Color string }

func (t reportTable) countsText() string {
	parts := make([]string, len(t.Counts))
	for i, c := range t.Counts {
		parts[i] = c.Text
	}
	return strings.Join(parts, " · ")
}

// reportGroups is the order of the tables in the job report, by job type.
var reportGroups = []struct{ jobType, title string }{
	{"backup", "Backups"},
	{"retention", "Retention"},
	{"check", "Checks"},
	{"restore", "Restores"},
}

// newJobReportEmail decides what the job report shows for the jobs that
// ended in (from, to]. jobs must not be empty. LogoSrc is left empty: the
// caller sets it.
func newJobReportEmail(jobs []repositories.JobReportRow, from, to time.Time, baseURL string) emailContent {
	counts := map[string]int{}
	for _, j := range jobs {
		counts[j.Status]++
	}
	total, failed := len(jobs), counts["failed"]

	// The zone is named once in the footer, unless the period spans a
	// daylight saving change: then the times in the summary name their zone.
	fromZone, toZone := zoneLabel(from.In(emailLocation)), zoneLabel(to.In(emailLocation))
	mixedZones := fromZone != toZone
	periodTime := func(t time.Time) string {
		if mixedZones {
			return formatEmailTime(t) + " " + zoneLabel(t.In(emailLocation))
		}
		return formatEmailTime(t)
	}

	e := emailContent{
		Title:       "Arkeep job report",
		StatusLabel: "Job report", StatusBG: orangeBG, StatusFG: orangeFG,
		Summary: fmt.Sprintf("Jobs that finished between %s and %s.", periodTime(from), periodTime(to)),
		Footer:  fmt.Sprintf(emailFooter, toZone),
	}
	if mixedZones {
		e.Footer = emailFooterPlain
	}

	switch {
	case failed > 0:
		e.StatusColor = red
		e.Headline = fmt.Sprintf("%d of %s failed", failed, plural(total, "job"))
		e.Title += ": " + e.Headline
	case counts["succeeded"] == total:
		e.StatusColor = greenStripe
		e.Headline = "All " + plural(total, "job") + " succeeded"
		if total == 1 {
			e.Headline = "1 job succeeded"
		}
	default: // cancelled or interrupted jobs, none failed
		e.StatusColor = amber
		e.Headline = plural(total, "job") + " finished"
	}
	e.Preheader = fmt.Sprintf("%s in the report: %d succeeded, %d failed.", plural(total, "job"), counts["succeeded"], failed)

	e.Metrics = []emailMetric{
		{Label: "Jobs", Value: fmt.Sprint(total)},
		{Label: "Succeeded", Value: fmt.Sprint(counts["succeeded"]), Color: green},
		{Label: "Failed", Value: fmt.Sprint(failed), Color: redFG},
	}
	if failed == 0 {
		e.Metrics[2].Color = ""
	}
	if n := counts["cancelled"]; n > 0 {
		e.Metrics = append(e.Metrics, emailMetric{Label: "Cancelled", Value: fmt.Sprint(n)})
	}
	if n := counts["interrupted"]; n > 0 {
		e.Metrics = append(e.Metrics, emailMetric{Label: "Interrupted", Value: fmt.Sprint(n), Color: amberFG})
	}

	// Started times carry a date when the jobs did not all start on the same
	// day, as in a daily report that spans midnight.
	withDate := false
	var day string
	for _, j := range jobs {
		if j.StartedAt == nil {
			continue
		}
		d := j.StartedAt.In(emailLocation).Format("2006-01-02")
		if day != "" && d != day {
			withDate = true
			break
		}
		day = d
	}

	type group struct {
		jobType, title string
		jobs           []repositories.JobReportRow
	}
	var groups []group
	for _, g := range reportGroups {
		var gj []repositories.JobReportRow
		for _, j := range jobs {
			if j.Type == g.jobType {
				gj = append(gj, j)
			}
		}
		if len(gj) > 0 {
			groups = append(groups, group{g.jobType, g.title, gj})
		}
	}
	sizes := make([]int, len(groups))
	for i, g := range groups {
		sizes[i] = len(g.jobs)
	}
	limits := reportRowLimits(sizes)
	for i, g := range groups {
		e.Reports = append(e.Reports, newReportTable(g.title, g.jobType, g.jobs, limits[i], withDate))
	}

	if baseURL != "" {
		e.ActionURL = strings.TrimRight(baseURL, "/") + "/jobs"
		e.ActionLabel = "Open jobs"
	}
	return e
}

// reportRowLimits splits maxReportRows among tables of the given sizes: the
// smaller tables are shown in full, what they leave is shared evenly among
// the larger ones, and no table gets more than maxTableRows.
func reportRowLimits(sizes []int) []int {
	order := make([]int, len(sizes))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return sizes[a] - sizes[b] })

	limits := make([]int, len(sizes))
	left := maxReportRows
	for k, i := range order {
		n := min(sizes[i], left/(len(order)-k), maxTableRows)
		limits[i] = n
		left -= n
	}
	return limits
}

// newReportTable builds the table of one job type, showing at most limit
// rows.
func newReportTable(title, jobType string, jobs []repositories.JobReportRow, limit int, withDate bool) reportTable {
	t := reportTable{Title: title, StartedWidth: 56}
	if withDate {
		t.StartedWidth = 92
	}
	if jobType == "backup" {
		t.ExtraLabel = "Added"
	}

	counts := map[string]int{}
	for _, j := range jobs {
		counts[j.Status]++
	}
	t.Counts = []reportCount{{Text: plural(len(jobs), "job")}}
	for _, s := range []struct{ status, color string }{
		{"succeeded", ""}, {"failed", redFG}, {"cancelled", ""}, {"interrupted", amberFG},
	} {
		if n := counts[s.status]; n > 0 {
			t.Counts = append(t.Counts, reportCount{Text: fmt.Sprintf("%d %s", n, s.status), Color: s.color})
		}
	}

	for i, j := range jobs {
		if i == limit {
			t.More = fmt.Sprintf("… and %d more, see the jobs page.", len(jobs)-limit)
			break
		}
		t.Rows = append(t.Rows, newReportRow(j, withDate))
	}
	return t
}

func newReportRow(j repositories.JobReportRow, withDate bool) reportRow {
	r := reportRow{Agent: j.AgentName, Started: "–", Duration: "–", Status: capitalize(j.Status), StatusFG: mutedFG}

	switch j.Type {
	case "retention", "check":
		r.Name = j.DestinationName
		if r.Name == "" {
			r.Name = "Deleted destination"
		}
	case "restore":
		r.Name = j.PolicyName
		if r.Name == "" {
			r.Name = "Imported snapshot"
		}
	default: // backup
		r.Name = j.PolicyName
		if r.Name == "" {
			r.Name = "Deleted policy"
		}
		// What was written, also when the job failed on other destinations;
		// "–" when no destination succeeded.
		if j.SucceededDestinations > 0 {
			r.Extra = formatEmailBytes(j.AddedBytes)
		} else {
			r.Extra = "–"
		}
	}

	if j.StartedAt != nil {
		layout := "15:04"
		if withDate {
			layout = "Jan 2, 15:04"
		}
		r.Started = j.StartedAt.In(emailLocation).Format(layout)
		if j.EndedAt != nil {
			r.Duration = formatEmailDuration(j.EndedAt.Sub(*j.StartedAt))
		}
	}

	switch j.Status {
	case "succeeded":
		r.StatusFG = green
	case "failed":
		r.StatusFG = redFG
	case "interrupted":
		r.StatusFG = amberFG
	}
	return r
}

// plural writes n with the noun, adding "s" unless n is 1.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ReportOutcome is what SendJobReport did.
type ReportOutcome int

const (
	// ReportSent means the email went out.
	ReportSent ReportOutcome = iota
	// ReportEmpty means no job ended in the period: there was nothing to send.
	ReportEmpty
	// ReportSkipped means the report could not be sent: SMTP is not
	// configured or there is no recipient. The jobs of the period belong in
	// the next report that is sent.
	ReportSkipped
)

// SendJobReport emails the job report for the jobs that ended in (from, to]
// to the notification recipients (the active admins when none are set).
// Nothing is sent while SMTP is not configured. The report has its own
// setting: it does not depend on the email channel switch, which only turns
// the per-event emails on and off, so the events can go to a webhook while
// the report goes by email. The policy override of per-job notifications
// does not apply either: the report lists every job. A failed send is
// returned as an error and is not retried here.
func (s *NotificationService) SendJobReport(ctx context.Context, from, to time.Time) (ReportOutcome, error) {
	if s.smtpMissing(ctx) {
		return ReportSkipped, nil
	}
	if s.jobRepo == nil {
		return ReportSkipped, errors.New("job report: no job repository")
	}

	jobs, err := s.jobRepo.ListEndedBetween(ctx, from, to)
	if err != nil {
		return ReportSkipped, fmt.Errorf("job report: %w", err)
	}
	if len(jobs) == 0 {
		return ReportEmpty, nil
	}

	recipients, err := s.emailRecipients(ctx)
	if err != nil {
		return ReportSkipped, fmt.Errorf("job report: %w", err)
	}
	if len(recipients) == 0 {
		return ReportSkipped, nil
	}

	e := newJobReportEmail(jobs, from, to, s.baseURL)
	text, html, err := renderEmailBodies(e)
	if err != nil {
		s.logger.Warn("failed to render job report email, sending it as plain text", zap.Error(err))
	}
	if err := s.email.Send(ctx, recipients, e.Title, text, html); err != nil {
		return ReportSkipped, fmt.Errorf("job report: %w", err)
	}
	return ReportSent, nil
}

// ServerZone names the server's time zone as the emails do (e.g. "CET",
// "UTC"), for the current time.
func ServerZone() string {
	return zoneLabel(time.Now().In(emailLocation))
}
