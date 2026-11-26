// Package jobreport sends the job report email on a schedule: one email that
// lists every job that finished since the previous report, instead of, or
// next to, one email per job.
//
// The report runs hourly at a set minute, or daily, weekly (on a set day of
// the week) or monthly (on a set day of the month, 1 to 28 so it exists in
// every month) at a set time, in the server's local time zone (TZ), like
// policy schedules. Its period ends at
// that time; the email goes out sendDelay later. The period always starts
// where the previous report ended: the end of the last period is stored as
// KeyLastSentAt, and jobs are selected by their end time. So
// nothing is listed twice or lost after server downtime or a change of the
// frequency, and a job that is still running when the report is sent is in
// the next one. A report that fell due while the server was down is sent
// when it starts again. A send that fails is retried (see retryDelays).
//
// Turning the report on starts the first period at that moment, not at the
// beginning of the job history. Turning it off forgets KeyLastSentAt.
package jobreport

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/notification"
)

// Setting keys (stored in the settings key-value table).
const (
	// KeyFrequency is how often the report is sent: FrequencyOff (default),
	// FrequencyHourly, FrequencyDaily, FrequencyWeekly or FrequencyMonthly.
	KeyFrequency = "notification.report.frequency"
	// KeyTime is the time of day of the daily, weekly and monthly report,
	// "HH:MM" in the server's time zone. Default DefaultTime.
	KeyTime = "notification.report.time"
	// KeyWeekday is the day of the week of the weekly report, 0 (Sunday) to
	// 6 (Saturday) like time.Weekday. Default DefaultWeekday.
	KeyWeekday = "notification.report.weekday"
	// KeyDay is the day of the month of the monthly report, 1 to MaxDay.
	// Default 1.
	KeyDay = "notification.report.day"
	// KeyMinute is the minute past the hour of the hourly report, 0 to 59.
	// Default 0 (on the full hour).
	KeyMinute = "notification.report.minute"
	// KeyLastSentAt is the end of the period of the previous report (RFC 3339,
	// UTC): the start of the next one. Set by this package only.
	KeyLastSentAt = "notification.report.last_sent_at"

	settingsPrefix = "notification.report."
)

// Report frequencies.
const (
	FrequencyOff     = "off"
	FrequencyHourly  = "hourly"
	FrequencyDaily   = "daily"
	FrequencyWeekly  = "weekly"
	FrequencyMonthly = "monthly"
)

// DefaultTime is the time of the daily, weekly and monthly report when none
// is set.
const DefaultTime = "07:00"

// DefaultWeekday is the day of the weekly report when none is set.
const DefaultWeekday = time.Monday

// MaxDay is the last day of the month the monthly report can be sent on: the
// last one that exists in every month.
const MaxDay = 28

// sendDelay is how long after the end of its period (the configured time) a
// report is sent. A job's end time is taken before the job is saved, and on
// SQLite the save may wait for the single connection (e.g. behind a VACUUM):
// without the delay, a job that ended just before the period's end but was
// saved after the report's query would fall into a period already reported.
const sendDelay = 30 * time.Second

// jobTag identifies the report's scheduled gocron entry, which Reload
// replaces. catchUpTag identifies the one-time run of a report that fell due
// while the server was down, retryTag the one-time retry of a failed send:
// Reload leaves both alone.
const (
	jobTag     = "job-report"
	catchUpTag = "job-report-catch-up"
	retryTag   = "job-report-retry"
)

// retryDelays are the waits before the retries of a report whose send failed,
// counted from each failure. The hourly report is retried once: anything that
// takes longer is covered by the next hourly report anyway. The weekly and
// monthly reports, whose next report is far off, are retried for longer.
// After the last retry the next scheduled report covers the period.
var retryDelays = map[string][]time.Duration{
	FrequencyHourly:  {5 * time.Minute},
	FrequencyDaily:   {5 * time.Minute, 30 * time.Minute},
	FrequencyWeekly:  {5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour},
	FrequencyMonthly: {5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour},
}

// ValidFrequency reports whether f is one of the report frequencies.
func ValidFrequency(f string) bool {
	switch f {
	case FrequencyOff, FrequencyHourly, FrequencyDaily, FrequencyWeekly, FrequencyMonthly:
		return true
	}
	return false
}

var timeOfDay = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

// ParseMinute parses the minute of the hourly report, 0 to 59.
func ParseMinute(s string) (int, error) {
	m, err := strconv.Atoi(s)
	if err != nil || !ValidMinute(m) {
		return 0, fmt.Errorf("invalid minute %q, want 0 to 59", s)
	}
	return m, nil
}

// ValidMinute reports whether m is a minute past the hour, 0 to 59.
func ValidMinute(m int) bool {
	return m >= 0 && m <= 59
}

// ParseWeekday parses the day of the weekly report, 0 (Sunday) to 6.
func ParseWeekday(s string) (time.Weekday, error) {
	d, err := strconv.Atoi(s)
	if err != nil || !ValidWeekday(d) {
		return 0, fmt.Errorf("invalid weekday %q, want 0 (Sunday) to 6 (Saturday)", s)
	}
	return time.Weekday(d), nil
}

// ValidWeekday reports whether d is a day of the week, 0 (Sunday) to 6.
func ValidWeekday(d int) bool {
	return d >= 0 && d <= 6
}

// ParseDay parses the day of the month of the monthly report, 1 to MaxDay.
func ParseDay(s string) (int, error) {
	d, err := strconv.Atoi(s)
	if err != nil || !ValidDay(d) {
		return 0, fmt.Errorf("invalid day of the month %q, want 1 to %d", s, MaxDay)
	}
	return d, nil
}

// ValidDay reports whether d is a day of the month of the monthly report, 1
// to MaxDay.
func ValidDay(d int) bool {
	return d >= 1 && d <= MaxDay
}

// ParseTime parses a time of day written as "HH:MM" (24-hour clock).
func ParseTime(s string) (hour, minute int, err error) {
	m := timeOfDay.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, fmt.Errorf("invalid time %q, want HH:MM", s)
	}
	hour, _ = strconv.Atoi(m[1])
	minute, _ = strconv.Atoi(m[2])
	return hour, minute, nil
}

// Sender sends the report for the jobs that ended in (from, to]. Satisfied by
// *notification.NotificationService.
type Sender interface {
	SendJobReport(ctx context.Context, from, to time.Time) (notification.ReportOutcome, error)
}

// settingsStore is the subset of the settings repository the service needs.
type settingsStore interface {
	GetMany(ctx context.Context, prefix string) ([]db.Setting, error)
	Set(ctx context.Context, key string, value db.EncryptedString) error
	Delete(ctx context.Context, key string) error
}

// config is the report configuration as stored in the settings.
type config struct {
	frequency string
	// hour and minute are the time of the daily, weekly and monthly report.
	hour, minute int
	// hourlyMinute is the minute past the hour of the hourly report.
	hourlyMinute int
	// weekday is the day of the weekly report.
	weekday time.Weekday
	// day is the day of the month of the monthly report.
	day int
	// lastSentAt is zero when not stored.
	lastSentAt time.Time
}

// Service schedules and sends the job report. Create it with New.
type Service struct {
	cron     gocron.Scheduler
	settings settingsStore
	sender   Sender
	logger   *zap.Logger
	location *time.Location
	now      func() time.Time

	// mu guards the report's settings and ctx: Start, Reload and a send
	// reading or saving its period hold it, but not while the email is sent,
	// so saving the settings never waits for the SMTP server.
	mu sync.Mutex
	// sendMu serialises the sends (scheduled, caught up and retries), so one
	// period is never reported twice at the same time. Held for a whole send.
	sendMu sync.Mutex
	// ctx is the context passed to Start, used by the scheduled runs.
	ctx context.Context
	// retryDelays is retryDelays; tests shorten it.
	retryDelays map[string][]time.Duration
	// retries counts the retries of the current period's send, reset by every
	// scheduled run and by every send that did not fail. Guarded by sendMu.
	retries int
}

// New creates the service. The schedule is evaluated in the server's local
// time zone.
func New(settings settingsStore, sender Sender, logger *zap.Logger) (*Service, error) {
	return newService(settings, sender, logger, time.Local, time.Now)
}

func newService(settings settingsStore, sender Sender, logger *zap.Logger, location *time.Location, now func() time.Time) (*Service, error) {
	c, err := gocron.NewScheduler(gocron.WithLocation(location))
	if err != nil {
		return nil, fmt.Errorf("failed to create gocron scheduler: %w", err)
	}
	return &Service{
		cron:     c,
		settings: settings,
		sender:   sender,
		logger:   logger.Named("job_report"),
		location: location,
		now:      now,
		ctx:      context.Background(),

		retryDelays: retryDelays,
	}, nil
}

// Start schedules the report as configured and starts the scheduler. When a
// report fell due while the server was down, it is sent right away. Call once
// at server startup.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = ctx

	cfg, err := s.prepare(ctx)
	if err != nil {
		return err
	}
	if err := s.schedule(cfg); err != nil {
		return err
	}
	// A report sent on time ends its period at its due time, so only a period
	// that ends earlier missed a report.
	if cfg.frequency != FrequencyOff && cfg.lastSentAt.Before(s.periodEnd(cfg)) {
		s.logger.Info("job report fell due while the server was down, sending it now")
		if _, err := s.cron.NewJob(
			gocron.OneTimeJob(gocron.OneTimeJobStartImmediately()),
			gocron.NewTask(s.run),
			gocron.WithTags(catchUpTag),
		); err != nil {
			return fmt.Errorf("failed to schedule overdue job report: %w", err)
		}
	}
	s.cron.Start()
	return nil
}

// Stop shuts the scheduler down, waiting for a report being sent.
func (s *Service) Stop() error {
	if err := s.cron.Shutdown(); err != nil {
		return fmt.Errorf("job report scheduler shutdown error: %w", err)
	}
	return nil
}

// Reload applies the stored settings after they were changed: it reschedules
// the report and starts or forgets its period. Called by the settings API.
func (s *Service) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cron.RemoveByTags(jobTag)
	cfg, err := s.prepare(ctx)
	if err != nil {
		return err
	}
	return s.schedule(cfg)
}

// prepare loads the configuration and starts or forgets the report's period:
// a report that is on and has no period yet starts it now, one that is off
// has none.
func (s *Service) prepare(ctx context.Context) (config, error) {
	cfg, err := s.load(ctx)
	if err != nil {
		return cfg, err
	}
	switch {
	case cfg.frequency == FrequencyOff:
		if err := s.settings.Delete(ctx, KeyLastSentAt); err != nil {
			return cfg, fmt.Errorf("failed to clear job report period: %w", err)
		}
		cfg.lastSentAt = time.Time{}
	case cfg.lastSentAt.IsZero():
		cfg.lastSentAt = s.now().UTC()
		if err := s.saveLastSentAt(ctx, cfg.lastSentAt); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

// schedule runs the report sendDelay after each end of a period.
func (s *Service) schedule(cfg config) error {
	delay := uint(sendDelay / time.Second)
	atTime := gocron.NewAtTimes(gocron.NewAtTime(uint(cfg.hour), uint(cfg.minute), delay))
	var def gocron.JobDefinition
	switch cfg.frequency {
	case FrequencyHourly:
		def = gocron.CronJob(fmt.Sprintf("%d %d * * * *", delay, cfg.hourlyMinute), true)
	case FrequencyDaily:
		def = gocron.DailyJob(1, atTime)
	case FrequencyWeekly:
		def = gocron.WeeklyJob(1, gocron.NewWeekdays(cfg.weekday), atTime)
	case FrequencyMonthly:
		def = gocron.MonthlyJob(1, gocron.NewDaysOfTheMonth(cfg.day), atTime)
	default:
		s.logger.Info("job report off")
		return nil
	}
	if _, err := s.cron.NewJob(def,
		gocron.NewTask(s.run),
		gocron.WithTags(jobTag),
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	); err != nil {
		return fmt.Errorf("failed to schedule job report: %w", err)
	}
	at := fmt.Sprintf("%02d:%02d", cfg.hour, cfg.minute)
	switch cfg.frequency {
	case FrequencyHourly:
		at = fmt.Sprintf("XX:%02d", cfg.hourlyMinute)
	case FrequencyWeekly:
		at = cfg.weekday.String() + " " + at
	case FrequencyMonthly:
		at = fmt.Sprintf("day %d %s", cfg.day, at)
	}
	s.logger.Info("job report scheduled", zap.String("frequency", cfg.frequency), zap.String("at", at))
	return nil
}

// run is a scheduled report (or one caught up at startup). A retry still
// pending from an earlier failed send is dropped: this run covers its period.
func (s *Service) run() {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.cron.RemoveByTags(retryTag)
	s.retries = 0
	s.send()
}

// retry sends a report again after a failed send.
func (s *Service) retry() {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.send()
}

// send sends the report for the jobs that ended since the previous one. The
// caller holds sendMu. The period only moves on once the report was sent, or
// when there was nothing to send: a report that could not be sent is covered
// by a retry or by the next one, which start at the same KeyLastSentAt and so
// lose or repeat nothing.
//
// The settings may change while the email is sent. The period is then saved
// only if the report is still on and its period still starts where this one
// did: a report turned off, or turned off and on again, keeps the period the
// change gave it.
func (s *Service) send() {
	s.mu.Lock()
	ctx := s.ctx
	cfg, err := s.load(ctx)
	s.mu.Unlock()
	if err != nil {
		s.logger.Error("failed to load job report settings", zap.Error(err))
		return
	}
	if cfg.frequency == FrequencyOff || cfg.lastSentAt.IsZero() {
		return
	}

	// Retries and a report caught up at startup cover the same period as the
	// report sent on time.
	from, to := cfg.lastSentAt, s.periodEnd(cfg)
	if !to.After(from) {
		// The period already ended before it started (the report was turned
		// on after the due time but before the send) or was reported (a
		// report caught up at startup right before a regular one).
		return
	}
	outcome, err := s.sender.SendJobReport(ctx, from, to)
	if err != nil {
		s.logger.Error("failed to send job report", zap.Error(err))
		s.scheduleRetry(cfg.frequency)
		return
	}
	s.retries = 0
	switch outcome {
	case notification.ReportSkipped:
		s.logger.Info("job report not sent: SMTP is not configured or there is no recipient")
		return
	case notification.ReportEmpty:
		s.logger.Debug("no job finished since the previous job report, nothing sent")
	default:
		s.logger.Info("job report sent", zap.Time("from", from), zap.Time("to", to))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now, err := s.load(ctx)
	if err != nil {
		s.logger.Error("failed to load job report settings", zap.Error(err))
		return
	}
	if now.frequency == FrequencyOff || !now.lastSentAt.Equal(from) {
		s.logger.Info("job report settings changed while the report was sent, keeping the new period")
		return
	}
	if err := s.saveLastSentAt(ctx, to); err != nil {
		s.logger.Error("failed to save job report period", zap.Error(err))
	}
}

// scheduleRetry schedules the next retry of a failed send, if any is left for
// the frequency. The caller holds sendMu.
func (s *Service) scheduleRetry(frequency string) {
	delays := s.retryDelays[frequency]
	if s.retries >= len(delays) {
		s.logger.Warn("job report not sent after retrying, the next scheduled report covers its period")
		return
	}
	delay := delays[s.retries]
	s.retries++
	// time.Now, not s.now: gocron runs on the real clock, while s.now only
	// places the report's periods (tests stop it at a fixed time).
	if _, err := s.cron.NewJob(
		gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(time.Now().Add(delay))),
		gocron.NewTask(s.retry),
		gocron.WithTags(retryTag),
	); err != nil {
		s.logger.Error("failed to schedule job report retry", zap.Error(err))
		return
	}
	s.logger.Info("job report retry scheduled", zap.Duration("in", delay), zap.Int("attempt", s.retries))
}

func (s *Service) load(ctx context.Context) (config, error) {
	cfg := config{frequency: FrequencyOff, weekday: DefaultWeekday, day: 1}
	settings, err := s.settings.GetMany(ctx, settingsPrefix)
	if err != nil {
		return cfg, fmt.Errorf("failed to load job report settings: %w", err)
	}
	idx := make(map[string]string, len(settings))
	for _, st := range settings {
		idx[st.Key] = string(st.Value)
	}

	if f := idx[KeyFrequency]; f != "" {
		if ValidFrequency(f) {
			cfg.frequency = f
		} else {
			s.logger.Warn("invalid job report frequency, report off", zap.String("frequency", f))
		}
	}
	t := idx[KeyTime]
	if t == "" {
		t = DefaultTime
	}
	if cfg.hour, cfg.minute, err = ParseTime(t); err != nil {
		s.logger.Warn("invalid job report time, using the default", zap.Error(err))
		cfg.hour, cfg.minute, _ = ParseTime(DefaultTime)
	}
	if v := idx[KeyMinute]; v != "" {
		if cfg.hourlyMinute, err = ParseMinute(v); err != nil {
			s.logger.Warn("invalid job report minute, using the full hour", zap.Error(err))
			cfg.hourlyMinute = 0
		}
	}
	if v := idx[KeyWeekday]; v != "" {
		if cfg.weekday, err = ParseWeekday(v); err != nil {
			s.logger.Warn("invalid job report weekday, using the default", zap.Error(err))
			cfg.weekday = DefaultWeekday
		}
	}
	if v := idx[KeyDay]; v != "" {
		if cfg.day, err = ParseDay(v); err != nil {
			s.logger.Warn("invalid job report day of the month, using the first", zap.Error(err))
			cfg.day = 1
		}
	}
	if v := idx[KeyLastSentAt]; v != "" {
		if cfg.lastSentAt, err = time.Parse(time.RFC3339Nano, v); err != nil {
			s.logger.Warn("invalid job report period start, starting a new period", zap.Error(err))
			cfg.lastSentAt = time.Time{}
		}
	}
	return cfg, nil
}

func (s *Service) saveLastSentAt(ctx context.Context, t time.Time) error {
	if err := s.settings.Set(ctx, KeyLastSentAt, db.EncryptedString(t.UTC().Format(time.RFC3339Nano))); err != nil {
		return fmt.Errorf("failed to save job report period: %w", err)
	}
	return nil
}

// periodEnd is the end of the period of the report sent now: the most recent
// due time that is at least sendDelay ago.
func (s *Service) periodEnd(cfg config) time.Time {
	return lastDue(cfg, s.now().Add(-sendDelay).In(s.location)).UTC()
}

// lastDue returns the most recent time at or before now at which a report
// was due. now must be in the schedule's location.
func lastDue(cfg config, now time.Time) time.Time {
	if cfg.frequency == FrequencyHourly {
		// Counted in UTC, not on the local clock: in the hour repeated when
		// daylight saving time ends, the local time XX:MM exists twice, and
		// time.Date would pick either. The zone's offset gives the minute past
		// the UTC hour that is XX:MM locally (the same minute in zones with a
		// whole-hour offset, another one in e.g. India, UTC+5:30).
		_, offset := now.Zone()
		minute := ((cfg.hourlyMinute-offset/60)%60 + 60) % 60
		due := now.UTC().Truncate(time.Hour).Add(time.Duration(minute) * time.Minute)
		if due.After(now) {
			due = due.Add(-time.Hour)
		}
		return due.In(now.Location())
	}
	// time.Date, not AddDate on a due time: the time of day stays the
	// configured one across a change of daylight saving time.
	y, mo, d := now.Date()
	at := func(mo time.Month, d int) time.Time {
		return time.Date(y, mo, d, cfg.hour, cfg.minute, 0, 0, now.Location())
	}
	switch cfg.frequency {
	case FrequencyWeekly:
		// The configured weekday of this week (today or earlier), or of the
		// previous one if its time has not come yet.
		back := (int(now.Weekday()) - int(cfg.weekday) + 7) % 7
		due := at(mo, d-back)
		if due.After(now) {
			due = at(mo, d-back-7)
		}
		return due
	case FrequencyMonthly:
		// day is at most MaxDay, so it exists in the previous month too.
		due := at(mo, cfg.day)
		if due.After(now) {
			due = at(mo-1, cfg.day)
		}
		return due
	}
	due := at(mo, d)
	if due.After(now) {
		due = at(mo, d-1)
	}
	return due
}
