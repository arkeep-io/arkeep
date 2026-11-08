package notification

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// Email for job notifications, as HTML and as plain text with the same
// content. templates/job.html is one html/template for every job type and
// outcome: each section is optional, and what goes into it (wording, colors,
// which figures) is decided here in Go.
//
// The design is fixed dark, with the colors of the .dark block in
// gui/src/style.css written inline. There is deliberately no light variant:
// dark-mode CSS rendered with partly wrong colors in Gmail. Outlook on the web
// recolors the email in its dark mode regardless: it rewrites background
// colors inline with !important and strips background images, so neither
// [data-ogsb] rules nor gradient backgrounds can undo it.

//go:embed templates/job.html
var jobTemplateSrc string

// logoPNG is attached to every HTML email and referenced as cid:arkeep-logo.
//
//go:embed templates/logo.png
var logoPNG []byte

const logoCID = "arkeep-logo"

// emailItem is one label/value pair, used for the key figures and the details.
type emailItem struct{ Label, Value string }

// destinationRow is one row of the destination list of a backup email.
type destinationRow struct {
	Name, Status, StatusFG, Meta, Error string
}

// jobEmail is everything job.html needs. Colors and wording are decided in Go,
// the template only prints them.
type jobEmail struct {
	Title, Preheader                             string
	StatusLabel, StatusColor, StatusBG, StatusFG string
	Headline, Summary                            string
	Metrics                                      []emailItem
	ErrorTitle, Error                            string
	Destinations                                 []destinationRow
	Details                                      []emailItem
	ActionURL, ActionLabel                       string
	Footer                                       string
	// LogoSrc is template.URL because html/template rejects cid: URLs in a
	// plain string. Empty hides the image.
	LogoSrc template.URL
}

const (
	green       = "#4ade80"
	greenStripe = "#15803d"
	greenBG     = "#12331f"
	red         = "#df202e"
	redFG       = "#ff8a90"
	redBG       = "#3a1417"
	mutedFG     = "#a39b96"
	// emailFooter names the zone of the times in the email; emailFooterPlain
	// is used when they span two zones (see newJobEmail).
	emailFooter      = "Sent by your Arkeep server. Times are in %s."
	emailFooterPlain = "Sent by your Arkeep server."
)

// emailLocation is the time zone of the times in the email: the server's
// local zone, which TZ sets, as for the schedules. Tests replace it.
var emailLocation = time.Local

// maxEmailError caps an error message shown in the email, so a long restic
// stderr does not turn it into pages of output.
const maxEmailError = 4000

// html/template strips HTML comments, so the Outlook conditionals must come
// from functions that return template.HTML. Only constants go in here.
var emailFuncs = template.FuncMap{
	"msoHead": func() template.HTML {
		return `<!--[if mso]><noscript><xml><o:OfficeDocumentSettings><o:PixelsPerInch>96</o:PixelsPerInch></o:OfficeDocumentSettings></xml></noscript><style>table,td,div,h1,p{font-family:Arial,sans-serif !important;}</style><![endif]-->`
	},
	"ghostOpen": func() template.HTML {
		return `<!--[if mso]><table role="presentation" width="600" border="0" cellpadding="0" cellspacing="0"><tr><td><![endif]-->`
	},
	"ghostClose": func() template.HTML {
		return `<!--[if mso]></td></tr></table><![endif]-->`
	},
}

// Never text/template: html/template is what escapes names and error messages.
var jobTemplate = template.Must(template.New("job.html").Funcs(emailFuncs).Parse(jobTemplateSrc))

// jobEmailData is what the email shows about a job. It is loaded from the
// database when the email is sent (see NotificationService.emailBodies), not
// stored in the notification payload, which also goes to webhooks.
type jobEmailData struct {
	JobID           string
	JobType         string // db.Job.Type
	PolicyName      string
	DestinationName string // set for jobs with exactly one destination
	AgentName       string
	StartedAt       *time.Time
	EndedAt         *time.Time
	Destinations    []destinationResult
	Error           string // with its line breaks
}

// destinationResult is the outcome of a job on one destination.
type destinationResult struct {
	Name   string
	Status string
	// SnapshotIDs holds the destination's snapshot and those of its command
	// sources, each of which is a snapshot of its own.
	SnapshotIDs []string
	// AddedBytes is the data newly added to the repository, not the size of
	// the backup: 0 when nothing changed.
	AddedBytes         int64
	StartedAt, EndedAt *time.Time
	Error              string
}

// jobEmailDataFrom builds the email data of a job loaded with
// JobRepository.GetByIDWithDetails.
func jobEmailDataFrom(job *repositories.JobWithNames, dests []repositories.JobDestinationWithName, cmds []repositories.JobDestinationCommandWithName) jobEmailData {
	data := jobEmailData{
		JobID:      job.ID.String(),
		JobType:    job.Type,
		PolicyName: job.PolicyName,
		AgentName:  job.AgentName,
		StartedAt:  job.StartedAt,
		EndedAt:    job.EndedAt,
		Error:      truncateEmailError(job.Error),
	}
	// Retention and check jobs run against exactly one destination.
	if len(dests) == 1 {
		data.DestinationName = dests[0].DestinationName
	}
	for _, d := range dests {
		r := destinationResult{
			Name:       d.DestinationName,
			Status:     d.Status,
			AddedBytes: d.SizeBytes,
			StartedAt:  d.StartedAt,
			EndedAt:    d.EndedAt,
			Error:      truncateEmailError(d.Error),
		}
		if d.SnapshotID != "" {
			r.SnapshotIDs = append(r.SnapshotIDs, d.SnapshotID)
		}
		for _, c := range cmds {
			if c.DestinationID != d.DestinationID {
				continue
			}
			if c.SnapshotID != "" {
				r.SnapshotIDs = append(r.SnapshotIDs, c.SnapshotID)
			}
			r.AddedBytes += c.SizeBytes
		}
		data.Destinations = append(data.Destinations, r)
	}
	return data
}

// jobEmailBodies renders the plain-text and the HTML body of the email for a
// job, both with the same content. at is when the notification was created,
// the end time when the job has none.
func jobEmailBodies(failed bool, title string, data jobEmailData, at time.Time, baseURL string) (text, html string, err error) {
	e := newJobEmail(title, failed, data, at, baseURL)
	text = renderJobEmailText(e)
	e.LogoSrc = template.URL("cid:" + logoCID)
	html, err = renderJobEmail(e)
	return text, html, err
}

// renderJobEmailText writes e as plain text, for mail clients that do not
// show HTML. Sections follow the order of templates/job.html.
func renderJobEmailText(e jobEmail) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }
	section := func() {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
	}
	// aligned writes items as "Label:  value" with the values in one column.
	aligned := func(items []emailItem) {
		width := 0
		for _, it := range items {
			width = max(width, utf8.RuneCountInString(it.Label))
		}
		for _, it := range items {
			pad := strings.Repeat(" ", width-utf8.RuneCountInString(it.Label))
			line(it.Label + ":" + pad + "  " + it.Value)
		}
	}
	indent := func(s, prefix string) string {
		return prefix + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n"+prefix)
	}

	line(e.Headline)
	if e.Summary != "" {
		line(e.Summary)
	}
	if len(e.Metrics) > 0 {
		section()
		aligned(e.Metrics)
	}
	if e.Error != "" {
		section()
		line(e.ErrorTitle + ":")
		line(indent(e.Error, "  "))
	}
	if len(e.Destinations) > 0 {
		section()
		line("Destinations:")
		for _, d := range e.Destinations {
			line("- " + d.Name + ": " + d.Status)
			if d.Meta != "" {
				line("  " + d.Meta)
			}
			if d.Error != "" {
				line(indent(d.Error, "    "))
			}
		}
	}
	if len(e.Details) > 0 {
		section()
		aligned(e.Details)
	}
	if e.ActionURL != "" {
		section()
		line(e.ActionLabel + ": " + e.ActionURL)
	}
	section()
	line("-- ")
	line(e.Footer)
	return b.String()
}

func renderJobEmail(e jobEmail) (string, error) {
	var buf bytes.Buffer
	if err := jobTemplate.Execute(&buf, e); err != nil {
		return "", fmt.Errorf("render job email: %w", err)
	}
	return buf.String(), nil
}

// newJobEmail decides what the email shows for the job. LogoSrc is left
// empty: the caller sets it, since it depends on how the email is sent.
func newJobEmail(title string, failed bool, p jobEmailData, at time.Time, baseURL string) jobEmail {
	operation, name, _ := JobSubject{Type: p.JobType, PolicyName: p.PolicyName, DestinationName: p.DestinationName}.describe()
	if name == "" {
		name = operation
	}

	ended := at
	if p.EndedAt != nil {
		ended = *p.EndedAt
	}
	var duration string
	if p.StartedAt != nil {
		duration = formatEmailDuration(ended.Sub(*p.StartedAt))
	}

	// The zone is named once in the footer, unless the job ran across a
	// daylight saving change: then each time names its own zone.
	endZone := zoneLabel(ended.In(emailLocation))
	mixedZones := p.StartedAt != nil && zoneLabel(p.StartedAt.In(emailLocation)) != endZone
	emailTime := func(t time.Time) string {
		if mixedZones {
			return formatEmailTime(t) + " " + zoneLabel(t.In(emailLocation))
		}
		return formatEmailTime(t)
	}

	e := jobEmail{Title: title, Footer: fmt.Sprintf(emailFooter, endZone)}
	if mixedZones {
		e.Footer = emailFooterPlain
	}
	if failed {
		e.StatusLabel, e.StatusColor, e.StatusBG, e.StatusFG = "Failed", red, redBG, redFG
		e.Headline = operation + " failed"
		e.ErrorTitle = "Error"
		e.Error = p.Error
	} else {
		e.StatusLabel, e.StatusColor, e.StatusBG, e.StatusFG = "Succeeded", greenStripe, greenBG, green
		e.Headline = operation + " completed"
	}

	if duration != "" {
		e.Metrics = append(e.Metrics, emailItem{"Duration", duration})
	}

	// Sentences after the first one of the preheader.
	var extra []string

	switch p.JobType {
	case "retention", "check":
		// One destination, no policy, no snapshot.
		e.Summary = joinNames(p.DestinationName, " by ", p.AgentName)
	case "restore":
		e.Summary = joinNames(p.PolicyName, " on ", p.AgentName)
		if p.DestinationName != "" {
			e.Details = append(e.Details, emailItem{"Destination", p.DestinationName})
		}
	default: // backup
		e.Summary = joinNames(p.PolicyName, " on ", p.AgentName)
		if len(p.Destinations) > 0 {
			var added int64
			succeeded := 0
			for _, d := range p.Destinations {
				added += d.AddedBytes
				if d.Status == "succeeded" {
					succeeded++
				}
				e.Destinations = append(e.Destinations, newDestinationRow(d))
			}
			counts := fmt.Sprintf("%d of %d", succeeded, len(p.Destinations))
			e.Metrics = append(e.Metrics, emailItem{"Added", formatEmailBytes(added)}, emailItem{"Destinations", counts})
			if failed {
				extra = append(extra, counts+" destinations succeeded.")
			} else {
				extra = append(extra, fmt.Sprintf("%s added, %s destinations succeeded.", formatEmailBytes(added), counts))
			}
		}
	}

	var first string
	switch {
	case failed && duration != "":
		first = fmt.Sprintf("%s failed after %s.", name, duration)
	case failed:
		first = name + " failed."
	case duration != "":
		first = fmt.Sprintf("%s finished in %s.", name, duration)
	default:
		first = name + " finished."
	}
	e.Preheader = strings.Join(append([]string{first}, extra...), " ")

	if p.StartedAt != nil {
		e.Details = append(e.Details, emailItem{"Started", emailTime(*p.StartedAt)})
	}
	e.Details = append(e.Details, emailItem{"Finished", emailTime(ended)})
	if p.JobID != "" {
		e.Details = append(e.Details, emailItem{"Job ID", p.JobID})
		if baseURL != "" {
			e.ActionURL = strings.TrimRight(baseURL, "/") + "/jobs/" + url.PathEscape(p.JobID)
			e.ActionLabel = "View job"
		}
	}
	return e
}

func newDestinationRow(d destinationResult) destinationRow {
	row := destinationRow{Name: d.Name, Status: capitalize(d.Status), StatusFG: mutedFG, Error: d.Error}
	if row.Name == "" {
		row.Name = "Deleted destination"
	}
	switch d.Status {
	case "succeeded":
		row.StatusFG = green
	case "failed":
		row.StatusFG = redFG
	}

	var meta []string
	short := make([]string, len(d.SnapshotIDs))
	for i, id := range d.SnapshotIDs {
		short[i] = shortSnapshotID(id)
	}
	switch {
	case len(short) == 1:
		meta = append(meta, "Snapshot "+short[0])
	case len(short) > 1:
		meta = append(meta, "Snapshots "+strings.Join(short, ", "))
	case d.Status == "failed":
		meta = append(meta, "No snapshot created")
	}
	if d.Status == "succeeded" {
		meta = append(meta, formatEmailBytes(d.AddedBytes)+" added")
	}
	if d.StartedAt != nil && d.EndedAt != nil {
		meta = append(meta, formatEmailDuration(d.EndedAt.Sub(*d.StartedAt)))
	}
	row.Meta = strings.Join(meta, " · ")
	return row
}

// joinNames returns "<name><sep><agent>", or whichever of the two is set.
func joinNames(name, sep, agent string) string {
	switch {
	case name != "" && agent != "":
		return name + sep + agent
	case name != "":
		return name
	default:
		return agent
	}
}

// shortSnapshotID shortens a restic snapshot ID to its usual 8 characters.
func shortSnapshotID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// formatEmailTime formats t in emailLocation like "Nov 5, 2026, 03:00",
// without the zone (see newJobEmail).
func formatEmailTime(t time.Time) string {
	return t.In(emailLocation).Format("Jan 2, 2006, 15:04")
}

// zoneLabel names the zone of t by its abbreviation from the time zone
// database (e.g. "CET"), or by its UTC offset (e.g. "UTC+4") for zones that
// have none, never by the IANA name.
func zoneLabel(t time.Time) string {
	name, offset := t.Zone()
	if name != "" && name[0] != '+' && name[0] != '-' {
		return name
	}
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	h, m := offset/3600, offset%3600/60
	if m != 0 {
		return fmt.Sprintf("UTC%s%d:%02d", sign, h, m)
	}
	return fmt.Sprintf("UTC%s%d", sign, h)
}

// formatEmailDuration matches formatDuration in gui/src/lib/jobUtils.ts.
func formatEmailDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int64(d / time.Second)
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	if m < 60 {
		return fmt.Sprintf("%dm %ds", m, s%60)
	}
	return fmt.Sprintf("%dh %dm", m/60, m%60)
}

// formatEmailBytes matches formatBytes in gui/src/lib/jobUtils.ts (1024-based,
// one decimal), except that plain bytes have no decimal.
func formatEmailBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", max(b, 0))
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(b) / 1024
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// truncateEmailError cuts s to maxEmailError bytes without splitting a rune.
func truncateEmailError(s string) string {
	if len(s) <= maxEmailError {
		return s
	}
	cut := maxEmailError
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
