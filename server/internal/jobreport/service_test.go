package jobreport

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/notification"
)

// fakeSettings is an in-memory settings repository.
type fakeSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func newFakeSettings(kv map[string]string) *fakeSettings {
	f := &fakeSettings{m: map[string]string{}}
	for k, v := range kv {
		f.m[k] = v
	}
	return f
}

func (f *fakeSettings) Set(_ context.Context, key string, value db.EncryptedString) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[key] = string(value)
	return nil
}

func (f *fakeSettings) GetMany(_ context.Context, prefix string) ([]db.Setting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []db.Setting
	for k, v := range f.m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, db.Setting{Key: k, Value: db.EncryptedString(v)})
		}
	}
	return out, nil
}

func (f *fakeSettings) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, key)
	return nil
}

func (f *fakeSettings) value(key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.m[key]
	return v, ok
}

// fakeSender records the periods it was asked to report.
type fakeSender struct {
	outcome notification.ReportOutcome
	err     error
	calls   chan [2]time.Time
}

func newFakeSender(outcome notification.ReportOutcome, err error) *fakeSender {
	return &fakeSender{outcome: outcome, err: err, calls: make(chan [2]time.Time, 4)}
}

func (f *fakeSender) SendJobReport(_ context.Context, from, to time.Time) (notification.ReportOutcome, error) {
	f.calls <- [2]time.Time{from, to}
	return f.outcome, f.err
}

var vienna = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		panic(err)
	}
	return loc
}()

// now is 2026-11-19 10:30 in Vienna (CET, UTC+1).
var now = time.Date(2026, 11, 19, 10, 30, 0, 0, vienna)

func newTestService(t *testing.T, settings *fakeSettings, sender Sender) *Service {
	t.Helper()
	return newTestServiceAt(t, settings, sender, now)
}

// newTestServiceAt creates a service whose clock stands at at.
func newTestServiceAt(t *testing.T, settings *fakeSettings, sender Sender, at time.Time) *Service {
	t.Helper()
	s, err := newService(settings, sender, zap.NewNop(), vienna, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop() })
	return s
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func TestParseTime(t *testing.T) {
	for _, s := range []string{"00:00", "07:05", "23:59"} {
		if _, _, err := ParseTime(s); err != nil {
			t.Errorf("ParseTime(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "7:00", "24:00", "12:60", "12:00:00", "noon"} {
		if _, _, err := ParseTime(s); err == nil {
			t.Errorf("ParseTime(%q) accepted, want an error", s)
		}
	}
}

func TestParseWeekday(t *testing.T) {
	for _, s := range []string{"0", "1", "6"} {
		if _, err := ParseWeekday(s); err != nil {
			t.Errorf("ParseWeekday(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "-1", "7", "monday"} {
		if _, err := ParseWeekday(s); err == nil {
			t.Errorf("ParseWeekday(%q) accepted, want an error", s)
		}
	}
}

func TestParseDay(t *testing.T) {
	for _, s := range []string{"1", "15", "28"} {
		if _, err := ParseDay(s); err != nil {
			t.Errorf("ParseDay(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "0", "29", "31", "first"} {
		if _, err := ParseDay(s); err == nil {
			t.Errorf("ParseDay(%q) accepted, want an error", s)
		}
	}
}

func TestParseMinute(t *testing.T) {
	for _, s := range []string{"0", "30", "59"} {
		if _, err := ParseMinute(s); err != nil {
			t.Errorf("ParseMinute(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "-1", "60", "xx"} {
		if _, err := ParseMinute(s); err == nil {
			t.Errorf("ParseMinute(%q) accepted, want an error", s)
		}
	}
}

// TestSchedule_NextRun checks the entry gocron actually runs: its next run is
// sendDelay after the configured minute or time of day, in the schedule's
// time zone.
func TestSchedule_NextRun(t *testing.T) {
	tests := []struct {
		name     string
		settings map[string]string
		within   time.Duration
		check    func(time.Time) bool
	}{
		{"hourly at XX:30", map[string]string{KeyFrequency: FrequencyHourly, KeyMinute: "30"}, time.Hour,
			func(n time.Time) bool { return n.Minute() == 30 && n.Second() == 30 }},
		{"daily at 07:15", map[string]string{KeyFrequency: FrequencyDaily, KeyTime: "07:15"}, 24 * time.Hour,
			func(n time.Time) bool { return n.Hour() == 7 && n.Minute() == 15 && n.Second() == 30 }},
		{"weekly on Wednesday at 07:15", map[string]string{KeyFrequency: FrequencyWeekly, KeyWeekday: "3", KeyTime: "07:15"}, 7 * 24 * time.Hour,
			func(n time.Time) bool {
				return n.Weekday() == time.Wednesday && n.Hour() == 7 && n.Minute() == 15 && n.Second() == 30
			}},
		{"weekly by default on Monday", map[string]string{KeyFrequency: FrequencyWeekly}, 7 * 24 * time.Hour,
			func(n time.Time) bool { return n.Weekday() == time.Monday && n.Hour() == 7 && n.Minute() == 0 }},
		{"monthly on day 5 at 07:15", map[string]string{KeyFrequency: FrequencyMonthly, KeyDay: "5", KeyTime: "07:15"}, 31 * 24 * time.Hour,
			func(n time.Time) bool { return n.Day() == 5 && n.Hour() == 7 && n.Minute() == 15 && n.Second() == 30 }},
		{"monthly by default on day 1", map[string]string{KeyFrequency: FrequencyMonthly}, 31 * 24 * time.Hour,
			func(n time.Time) bool { return n.Day() == 1 && n.Hour() == 7 && n.Minute() == 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The period starts now: nothing is overdue at Start.
			tt.settings[KeyLastSentAt] = rfc(now)
			s := newTestService(t, newFakeSettings(tt.settings), newFakeSender(notification.ReportSent, nil))
			if err := s.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			jobs := s.cron.Jobs()
			if len(jobs) != 1 {
				t.Fatalf("%d scheduled entries, want 1", len(jobs))
			}
			next, err := jobs[0].NextRun()
			if err != nil {
				t.Fatalf("NextRun: %v", err)
			}
			next = next.In(vienna)
			if !tt.check(next) || time.Until(next) > tt.within || time.Until(next) < 0 {
				t.Errorf("next run %v, want the configured time within %v", next, tt.within)
			}
		})
	}
}

func TestLastDue(t *testing.T) {
	tests := []struct {
		name string
		cfg  config
		want time.Time
	}{
		{"hourly", config{frequency: FrequencyHourly}, time.Date(2026, 11, 19, 10, 0, 0, 0, vienna)},
		{"hourly at an earlier minute", config{frequency: FrequencyHourly, hourlyMinute: 15}, time.Date(2026, 11, 19, 10, 15, 0, 0, vienna)},
		{"hourly at a later minute", config{frequency: FrequencyHourly, hourlyMinute: 45}, time.Date(2026, 11, 19, 9, 45, 0, 0, vienna)},
		{"daily, earlier today", config{frequency: FrequencyDaily, hour: 7}, time.Date(2026, 11, 19, 7, 0, 0, 0, vienna)},
		{"daily, later today", config{frequency: FrequencyDaily, hour: 18, minute: 30}, time.Date(2026, 11, 18, 18, 30, 0, 0, vienna)},
		{"daily, right now", config{frequency: FrequencyDaily, hour: 10, minute: 30}, now},
		// now is a Thursday.
		{"weekly, earlier this week", config{frequency: FrequencyWeekly, weekday: time.Monday, hour: 7}, time.Date(2026, 11, 16, 7, 0, 0, 0, vienna)},
		{"weekly, today earlier", config{frequency: FrequencyWeekly, weekday: time.Thursday, hour: 7}, time.Date(2026, 11, 19, 7, 0, 0, 0, vienna)},
		{"weekly, today later", config{frequency: FrequencyWeekly, weekday: time.Thursday, hour: 18}, time.Date(2026, 11, 12, 18, 0, 0, 0, vienna)},
		{"weekly, later this week", config{frequency: FrequencyWeekly, weekday: time.Saturday, hour: 7}, time.Date(2026, 11, 14, 7, 0, 0, 0, vienna)},
		{"weekly, Sunday", config{frequency: FrequencyWeekly, weekday: time.Sunday, hour: 7}, time.Date(2026, 11, 15, 7, 0, 0, 0, vienna)},
		{"weekly, right now", config{frequency: FrequencyWeekly, weekday: time.Thursday, hour: 10, minute: 30}, now},
		{"monthly, earlier this month", config{frequency: FrequencyMonthly, day: 1, hour: 7}, time.Date(2026, 11, 1, 7, 0, 0, 0, vienna)},
		{"monthly, today earlier", config{frequency: FrequencyMonthly, day: 19, hour: 7}, time.Date(2026, 11, 19, 7, 0, 0, 0, vienna)},
		{"monthly, today later", config{frequency: FrequencyMonthly, day: 19, hour: 18}, time.Date(2026, 10, 19, 18, 0, 0, 0, vienna)},
		{"monthly, later this month", config{frequency: FrequencyMonthly, day: 28, hour: 7}, time.Date(2026, 10, 28, 7, 0, 0, 0, vienna)},
		{"monthly, right now", config{frequency: FrequencyMonthly, day: 19, hour: 10, minute: 30}, now},
	}
	for _, tt := range tests {
		if got := lastDue(tt.cfg, now); !got.Equal(tt.want) {
			t.Errorf("%s: lastDue = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestLastDue_AcrossBoundaries checks the weekly and monthly report across a
// change of year and of daylight saving time: the due time stays at the
// configured time of day.
func TestLastDue_AcrossBoundaries(t *testing.T) {
	tests := []struct {
		name string
		cfg  config
		now  time.Time
		want time.Time
	}{
		{"monthly, previous year", config{frequency: FrequencyMonthly, day: 15, hour: 7},
			time.Date(2027, 1, 10, 12, 0, 0, 0, vienna), time.Date(2026, 12, 15, 7, 0, 0, 0, vienna)},
		{"monthly, day 28 in February", config{frequency: FrequencyMonthly, day: 28, hour: 7},
			time.Date(2027, 3, 1, 12, 0, 0, 0, vienna), time.Date(2027, 2, 28, 7, 0, 0, 0, vienna)},
		{"weekly, previous year", config{frequency: FrequencyWeekly, weekday: time.Wednesday, hour: 7},
			time.Date(2027, 1, 2, 12, 0, 0, 0, vienna), time.Date(2026, 12, 30, 7, 0, 0, 0, vienna)},
		// 2026-10-25: the clocks go back from CEST to CET.
		{"weekly, across the end of DST", config{frequency: FrequencyWeekly, weekday: time.Friday, hour: 7},
			time.Date(2026, 10, 29, 12, 0, 0, 0, vienna), time.Date(2026, 10, 23, 7, 0, 0, 0, vienna)},
		{"monthly, across the end of DST", config{frequency: FrequencyMonthly, day: 20, hour: 7},
			time.Date(2026, 11, 10, 12, 0, 0, 0, vienna), time.Date(2026, 10, 20, 7, 0, 0, 0, vienna)},
	}
	for _, tt := range tests {
		got := lastDue(tt.cfg, tt.now)
		if !got.Equal(tt.want) || got.Hour() != tt.cfg.hour {
			t.Errorf("%s: lastDue = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestLastDue_Hourly(t *testing.T) {
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-10-25 in Vienna: at 03:00 CEST the clocks go back to 02:00 CET, so
	// 02:15 exists twice: at 00:15 UTC (CEST) and at 01:15 UTC (CET).
	tests := []struct {
		name   string
		minute int
		now    time.Time
		want   time.Time
	}{
		{"first 02:15 (CEST)", 15, time.Date(2026, 10, 25, 0, 20, 0, 0, time.UTC).In(vienna), time.Date(2026, 10, 25, 0, 15, 0, 0, time.UTC)},
		{"second 02:15 (CET)", 15, time.Date(2026, 10, 25, 1, 20, 0, 0, time.UTC).In(vienna), time.Date(2026, 10, 25, 1, 15, 0, 0, time.UTC)},
		{"between the two 02:15", 15, time.Date(2026, 10, 25, 1, 10, 0, 0, time.UTC).In(vienna), time.Date(2026, 10, 25, 0, 15, 0, 0, time.UTC)},
		{"half-hour zone, full hour", 0, time.Date(2026, 11, 19, 10, 40, 0, 0, kolkata), time.Date(2026, 11, 19, 10, 0, 0, 0, kolkata)},
		{"half-hour zone, later minute", 45, time.Date(2026, 11, 19, 10, 40, 0, 0, kolkata), time.Date(2026, 11, 19, 9, 45, 0, 0, kolkata)},
		{"half-hour zone, right now", 40, time.Date(2026, 11, 19, 10, 40, 0, 0, kolkata), time.Date(2026, 11, 19, 10, 40, 0, 0, kolkata)},
	}
	for _, tt := range tests {
		got := lastDue(config{frequency: FrequencyHourly, hourlyMinute: tt.minute}, tt.now)
		if !got.Equal(tt.want) {
			t.Errorf("%s: lastDue = %v, want %v", tt.name, got.UTC(), tt.want.UTC())
		}
	}
}

func TestReload_StartsAndForgetsThePeriod(t *testing.T) {
	ctx := context.Background()
	settings := newFakeSettings(map[string]string{KeyFrequency: FrequencyDaily})
	s := newTestService(t, settings, newFakeSender(notification.ReportSent, nil))

	// Turned on: the first period starts now, not at the start of the history.
	if err := s.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if v, _ := settings.value(KeyLastSentAt); v != rfc(now) {
		t.Errorf("last_sent_at = %q after turning the report on, want %q", v, rfc(now))
	}

	// A stored period is kept when only the schedule changes.
	earlier := rfc(now.Add(-5 * time.Hour))
	_ = settings.Set(ctx, KeyLastSentAt, db.EncryptedString(earlier))
	_ = settings.Set(ctx, KeyFrequency, FrequencyHourly)
	if err := s.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if v, _ := settings.value(KeyLastSentAt); v != earlier {
		t.Errorf("last_sent_at = %q after changing the frequency, want it kept (%q)", v, earlier)
	}

	// Turned off: the period is forgotten.
	_ = settings.Set(ctx, KeyFrequency, FrequencyOff)
	if err := s.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if v, ok := settings.value(KeyLastSentAt); ok {
		t.Errorf("last_sent_at = %q after turning the report off, want none", v)
	}
}

func TestRun(t *testing.T) {
	last := now.Add(-24 * time.Hour)
	tests := []struct {
		name      string
		outcome   notification.ReportOutcome
		err       error
		wantMoved bool
	}{
		{"sent", notification.ReportSent, nil, true},
		{"nothing to send", notification.ReportEmpty, nil, true},
		{"email off or SMTP missing", notification.ReportSkipped, nil, false},
		{"send failed", notification.ReportSkipped, errors.New("smtp down"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := newFakeSettings(map[string]string{KeyFrequency: FrequencyDaily, KeyLastSentAt: rfc(last)})
			sender := newFakeSender(tt.outcome, tt.err)
			s := newTestService(t, settings, sender)

			s.run()

			// The period ends at the due time of the daily report, 07:00.
			end := time.Date(2026, 11, 19, 7, 0, 0, 0, vienna)
			call := <-sender.calls
			if !call[0].Equal(last) || !call[1].Equal(end) {
				t.Errorf("reported period %v – %v, want %v – %v", call[0], call[1], last, end)
			}
			want := rfc(last)
			if tt.wantMoved {
				want = rfc(end)
			}
			if v, _ := settings.value(KeyLastSentAt); v != want {
				t.Errorf("last_sent_at = %q, want %q", v, want)
			}
		})
	}
}

// TestRun_AtSendTime checks the report sent on time, sendDelay after 07:00:
// its period ends at 07:00, so a job that ended just before is in it.
func TestRun_AtSendTime(t *testing.T) {
	due := time.Date(2026, 11, 19, 7, 0, 0, 0, vienna)
	last := due.AddDate(0, 0, -1)
	settings := newFakeSettings(map[string]string{KeyFrequency: FrequencyDaily, KeyLastSentAt: rfc(last)})
	sender := newFakeSender(notification.ReportSent, nil)
	s := newTestServiceAt(t, settings, sender, due.Add(sendDelay))

	s.run()

	call := <-sender.calls
	if !call[0].Equal(last) || !call[1].Equal(due) {
		t.Errorf("reported period %v – %v, want %v – %v", call[0], call[1], last, due)
	}
	if v, _ := settings.value(KeyLastSentAt); v != rfc(due) {
		t.Errorf("last_sent_at = %q, want %q", v, rfc(due))
	}
}

func TestRun_PeriodStartedAfterDueTime(t *testing.T) {
	// The report was turned on at 07:00:10, after the due time but before the
	// send: the 07:00 report has nothing to cover.
	due := time.Date(2026, 11, 19, 7, 0, 0, 0, vienna)
	last := due.Add(10 * time.Second)
	settings := newFakeSettings(map[string]string{KeyFrequency: FrequencyDaily, KeyLastSentAt: rfc(last)})
	sender := newFakeSender(notification.ReportSent, nil)
	s := newTestServiceAt(t, settings, sender, due.Add(sendDelay))

	s.run()

	select {
	case call := <-sender.calls:
		t.Errorf("report sent for %v – %v, want none", call[0], call[1])
	default:
	}
	if v, _ := settings.value(KeyLastSentAt); v != rfc(last) {
		t.Errorf("last_sent_at = %q, want it kept at %q (never moved back)", v, rfc(last))
	}
}

// scriptedSender fails the sends listed in fail (1-based call numbers) and
// records the period of every call.
type scriptedSender struct {
	mu    sync.Mutex
	n     int
	fail  map[int]bool
	calls chan [2]time.Time
}

func (f *scriptedSender) SendJobReport(_ context.Context, from, to time.Time) (notification.ReportOutcome, error) {
	f.mu.Lock()
	f.n++
	failed := f.fail[f.n]
	f.mu.Unlock()
	f.calls <- [2]time.Time{from, to}
	if failed {
		return notification.ReportSkipped, errors.New("smtp down")
	}
	return notification.ReportSent, nil
}

func TestRetry(t *testing.T) {
	last := now.Add(-24 * time.Hour)
	// The due times of the daily report (07:00) and the hourly one (XX:00).
	daily := time.Date(2026, 11, 19, 7, 0, 0, 0, vienna)
	hourly := time.Date(2026, 11, 19, 10, 0, 0, 0, vienna)
	tests := []struct {
		name      string
		frequency string
		fail      map[int]bool
		wantCalls int
		wantMoved bool
	}{
		{"daily: sent on the second retry", FrequencyDaily, map[int]bool{1: true, 2: true}, 3, true},
		{"daily: gives up after two retries", FrequencyDaily, map[int]bool{1: true, 2: true, 3: true}, 3, false},
		{"hourly: sent on the retry", FrequencyHourly, map[int]bool{1: true}, 2, true},
		{"hourly: gives up after one retry", FrequencyHourly, map[int]bool{1: true, 2: true}, 2, false},
		{"no retry when the send works", FrequencyDaily, nil, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := newFakeSettings(map[string]string{KeyFrequency: tt.frequency, KeyLastSentAt: rfc(last)})
			sender := &scriptedSender{fail: tt.fail, calls: make(chan [2]time.Time, 8)}
			s := newTestService(t, settings, sender)
			s.retryDelays = map[string][]time.Duration{
				FrequencyHourly: {10 * time.Millisecond},
				FrequencyDaily:  {10 * time.Millisecond, 10 * time.Millisecond},
			}
			s.cron.Start()
			end := daily
			if tt.frequency == FrequencyHourly {
				end = hourly
			}

			s.run()

			for i := range tt.wantCalls {
				select {
				case call := <-sender.calls:
					// Every attempt reports the same period: it starts at
					// last_sent_at, which a failed send leaves alone.
					if !call[0].Equal(last) || !call[1].Equal(end) {
						t.Errorf("attempt %d reported %v – %v, want %v – %v", i+1, call[0], call[1], last, end)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("attempt %d did not happen", i+1)
				}
			}
			select {
			case <-sender.calls:
				t.Errorf("more than %d attempts", tt.wantCalls)
			case <-time.After(100 * time.Millisecond):
			}

			s.sendMu.Lock() // the last attempt may still be saving
			defer s.sendMu.Unlock()
			want := rfc(last)
			if tt.wantMoved {
				want = rfc(end)
			}
			if v, _ := settings.value(KeyLastSentAt); v != want {
				t.Errorf("last_sent_at = %q, want %q", v, want)
			}
		})
	}
}

func TestRun_DropsPendingRetry(t *testing.T) {
	settings := newFakeSettings(map[string]string{KeyFrequency: FrequencyDaily, KeyLastSentAt: rfc(now.Add(-24 * time.Hour))})
	sender := &scriptedSender{fail: map[int]bool{1: true}, calls: make(chan [2]time.Time, 8)}
	s := newTestService(t, settings, sender)

	s.run() // fails: a retry is scheduled (the scheduler is not started)
	<-sender.calls
	s.run() // the next scheduled report covers the period
	<-sender.calls

	for _, j := range s.cron.Jobs() {
		for _, tag := range j.Tags() {
			if tag == retryTag {
				t.Error("a retry is still scheduled after the next scheduled report")
			}
		}
	}
	if s.retries != 0 {
		t.Errorf("retries = %d after a scheduled report, want 0", s.retries)
	}
}

func TestStart_SendsOverdueReport(t *testing.T) {
	ctx := context.Background()

	t.Run("overdue", func(t *testing.T) {
		// The 07:00 report of today was missed.
		last := time.Date(2026, 11, 18, 7, 0, 0, 0, vienna)
		settings := newFakeSettings(map[string]string{KeyFrequency: FrequencyDaily, KeyTime: "07:00", KeyLastSentAt: rfc(last)})
		sender := newFakeSender(notification.ReportSent, nil)
		s := newTestService(t, settings, sender)
		if err := s.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		select {
		case call := <-sender.calls:
			if !call[0].Equal(last) {
				t.Errorf("overdue report starts at %v, want %v", call[0], last)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the overdue report was not sent")
		}
	})

	notDue := []struct {
		name string
		at   time.Time
		last time.Time
	}{
		// Today's 07:00 report was sent: its period ended at 07:00.
		{"sent today", now, time.Date(2026, 11, 19, 7, 0, 0, 0, vienna)},
		// Started at 07:00:10: the 07:00 report is sent on time, at 07:00:30.
		{"before its send time", time.Date(2026, 11, 19, 7, 0, 10, 0, vienna), time.Date(2026, 11, 18, 7, 0, 0, 0, vienna)},
	}
	for _, tt := range notDue {
		t.Run("not due: "+tt.name, func(t *testing.T) {
			settings := newFakeSettings(map[string]string{KeyFrequency: FrequencyDaily, KeyTime: "07:00", KeyLastSentAt: rfc(tt.last)})
			sender := newFakeSender(notification.ReportSent, nil)
			s := newTestServiceAt(t, settings, sender, tt.at)
			if err := s.Start(ctx); err != nil {
				t.Fatalf("Start: %v", err)
			}
			select {
			case <-sender.calls:
				t.Error("a report was sent at startup although none was due")
			case <-time.After(300 * time.Millisecond):
			}
		})
	}
}

// blockingSender holds every send until release is closed, after telling
// started that it began.
type blockingSender struct {
	started chan struct{}
	release chan struct{}
}

func (f *blockingSender) SendJobReport(context.Context, time.Time, time.Time) (notification.ReportOutcome, error) {
	f.started <- struct{}{}
	<-f.release
	return notification.ReportSent, nil
}

// TestSend_SettingsChangeDuringSend checks that saving the settings does not
// wait for a send, and that the send then keeps the period the change gave.
func TestSend_SettingsChangeDuringSend(t *testing.T) {
	ctx := context.Background()
	last := now.Add(-24 * time.Hour)
	end := time.Date(2026, 11, 19, 7, 0, 0, 0, vienna)
	tests := []struct {
		name   string
		change map[string]string // settings set while the email is sent
		want   string            // last_sent_at afterwards, "" for none
	}{
		{"time changed: the period moves on", map[string]string{KeyTime: "08:00"}, rfc(end)},
		{"turned off: no period", map[string]string{KeyFrequency: FrequencyOff}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := newFakeSettings(map[string]string{KeyFrequency: FrequencyDaily, KeyLastSentAt: rfc(last)})
			sender := &blockingSender{started: make(chan struct{}, 1), release: make(chan struct{})}
			s := newTestService(t, settings, sender)

			done := make(chan struct{})
			go func() { s.run(); close(done) }()
			<-sender.started

			for k, v := range tt.change {
				_ = settings.Set(ctx, k, db.EncryptedString(v))
			}
			reloaded := make(chan error, 1)
			go func() { reloaded <- s.Reload(ctx) }()
			select {
			case err := <-reloaded:
				if err != nil {
					t.Fatalf("Reload: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Reload waited for the send")
			}

			close(sender.release)
			<-done
			v, ok := settings.value(KeyLastSentAt)
			if tt.want == "" && ok {
				t.Errorf("last_sent_at = %q, want none", v)
			}
			if tt.want != "" && v != tt.want {
				t.Errorf("last_sent_at = %q, want %q", v, tt.want)
			}
		})
	}

	t.Run("turned off and on: the new period is kept", func(t *testing.T) {
		settings := newFakeSettings(map[string]string{KeyFrequency: FrequencyDaily, KeyLastSentAt: rfc(last)})
		sender := &blockingSender{started: make(chan struct{}, 1), release: make(chan struct{})}
		s := newTestService(t, settings, sender)

		done := make(chan struct{})
		go func() { s.run(); close(done) }()
		<-sender.started

		_ = settings.Set(ctx, KeyFrequency, FrequencyOff)
		if err := s.Reload(ctx); err != nil {
			t.Fatalf("Reload: %v", err)
		}
		_ = settings.Set(ctx, KeyFrequency, FrequencyDaily)
		if err := s.Reload(ctx); err != nil { // starts a new period now
			t.Fatalf("Reload: %v", err)
		}

		close(sender.release)
		<-done
		if v, _ := settings.value(KeyLastSentAt); v != rfc(now) {
			t.Errorf("last_sent_at = %q, want the new period %q", v, rfc(now))
		}
	})
}
