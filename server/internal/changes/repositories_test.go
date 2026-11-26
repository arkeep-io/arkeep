package changes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// outcome is what the fake repositories' writes return: an error, or
// whether they changed something (the bool and the row count of the writes
// that report it).
type outcome struct {
	err  error
	done bool
}

func (o *outcome) rows() int64 {
	if o.done {
		return 3
	}
	return 0
}

// The fakes implement the writes the decorators wrap; any other call panics
// on the nil embedded interface.
type fakeAgents struct {
	repositories.AgentRepository
	*outcome
}

func (f fakeAgents) Create(context.Context, *db.Agent) error { return f.err }
func (f fakeAgents) Update(context.Context, *db.Agent) error { return f.err }
func (f fakeAgents) UpdateStatus(context.Context, uuid.UUID, string, time.Time) error {
	return f.err
}
func (f fakeAgents) MarkOfflineIfStale(context.Context, uuid.UUID, time.Time) (bool, error) {
	return f.done, f.err
}
func (f fakeAgents) Delete(context.Context, uuid.UUID) error { return f.err }
func (f fakeAgents) BindCertFingerprint(context.Context, uuid.UUID, string) (bool, error) {
	return f.done, f.err
}
func (f fakeAgents) ResetCertFingerprint(context.Context, uuid.UUID) error { return f.err }

type fakePolicies struct {
	repositories.PolicyRepository
	*outcome
}

func (f fakePolicies) Create(context.Context, *db.Policy) error { return f.err }
func (f fakePolicies) Update(context.Context, *db.Policy) error { return f.err }
func (f fakePolicies) Delete(context.Context, uuid.UUID) error  { return f.err }
func (f fakePolicies) UpdateSchedule(context.Context, uuid.UUID, time.Time, time.Time) error {
	return f.err
}
func (f fakePolicies) UpdateNextRunAt(context.Context, uuid.UUID, time.Time) error { return f.err }
func (f fakePolicies) CreateWithDestinations(context.Context, *db.Policy, []db.PolicyDestination) error {
	return f.err
}
func (f fakePolicies) UpdateWithDestinations(context.Context, *db.Policy, []db.PolicyDestination) error {
	return f.err
}
func (f fakePolicies) AddDestination(context.Context, *db.PolicyDestination) error { return f.err }
func (f fakePolicies) RemoveDestination(context.Context, uuid.UUID, uuid.UUID) error {
	return f.err
}
func (f fakePolicies) DeleteDestinationAssociations(context.Context, uuid.UUID) error { return f.err }

type fakeDestinations struct {
	repositories.DestinationRepository
	*outcome
}

func (f fakeDestinations) Create(context.Context, *db.Destination) error { return f.err }
func (f fakeDestinations) Update(context.Context, *db.Destination) error { return f.err }
func (f fakeDestinations) UpdateRepoSize(context.Context, uuid.UUID, int64, time.Time) error {
	return f.err
}
func (f fakeDestinations) MarkRepoInitialized(context.Context, uuid.UUID, time.Time) error {
	return f.err
}
func (f fakeDestinations) ResetRepoInitialized(context.Context, uuid.UUID) error { return f.err }
func (f fakeDestinations) Delete(context.Context, uuid.UUID) error               { return f.err }
func (f fakeDestinations) UpdateLastCheck(context.Context, uuid.UUID, uuid.UUID, string, time.Time) error {
	return f.err
}
func (f fakeDestinations) TryAcquireBusyAll(context.Context, []uuid.UUID, uuid.UUID, string) (bool, error) {
	return f.done, f.err
}
func (f fakeDestinations) ReleaseBusy(context.Context, uuid.UUID, uuid.UUID) error { return f.err }
func (f fakeDestinations) ReleaseBusyForJobs(context.Context, []uuid.UUID) error   { return f.err }
func (f fakeDestinations) ReleaseStaleBusy(context.Context) (int64, error) {
	return f.rows(), f.err
}
func (f fakeDestinations) StartMaintenance(context.Context, uuid.UUID, string) error { return f.err }
func (f fakeDestinations) EndMaintenance(context.Context, uuid.UUID) error           { return f.err }
func (f fakeDestinations) ClaimForRepair(context.Context, uuid.UUID, uuid.UUID, string) (bool, error) {
	return f.done, f.err
}
func (f fakeDestinations) ClearEndedMaintenance(context.Context) (int64, error) {
	return f.rows(), f.err
}

type fakeSnapshots struct {
	repositories.SnapshotRepository
	*outcome
}

func (f fakeSnapshots) Create(context.Context, *db.Snapshot) error { return f.err }
func (f fakeSnapshots) Delete(context.Context, uuid.UUID) error    { return f.err }
func (f fakeSnapshots) DeleteStaleByDestination(context.Context, uuid.UUID, []string, time.Time) (int64, error) {
	return f.rows(), f.err
}
func (f fakeSnapshots) FillMissingSummary(context.Context, uuid.UUID, string, int64, db.SnapshotSummary) (bool, error) {
	return f.done, f.err
}

type fakeUsers struct {
	repositories.UserRepository
	*outcome
}

func (f fakeUsers) Create(context.Context, *db.User) error  { return f.err }
func (f fakeUsers) Update(context.Context, *db.User) error  { return f.err }
func (f fakeUsers) Delete(context.Context, uuid.UUID) error { return f.err }

type fakeAudit struct {
	repositories.AuditRepository
	*outcome
}

func (f fakeAudit) Create(context.Context, *db.AuditLog) error { return f.err }

// published is a change as a client receives it.
type published struct{ topic, changeType, id string }

func (r *recorder) takeChanges(t *testing.T) []published {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var got []published
	for i, msg := range r.sent {
		change, ok := msg.Payload.(websocket.Change)
		if msg.Type != websocket.MsgChange || !ok {
			t.Fatalf("published %q on %q, want a change", msg.Type, r.tops[i])
		}
		got = append(got, published{r.tops[i], change.Type, change.ID})
	}
	r.sent, r.tops = nil, nil
	return got
}

// write is one write of a decorator. id is the ID its change carries ("" for
// several objects); reports is true for the writes that say whether they
// changed something, which publish nothing when they did not.
type write struct {
	name    string
	call    func() error
	id      string
	reports bool
}

// TestDecorators: each write publishes one change of its type, on the topic
// of the type, once it succeeded; a failed write, or one that changed
// nothing, publishes nothing.
func TestDecorators(t *testing.T) {
	ctx := context.Background()
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	other := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	now := time.Now()
	ignore := func(_ any, err error) error { return err }

	result := &outcome{}
	rec := &recorder{}
	agents := NewAgentRepository(fakeAgents{outcome: result}, rec)
	policies := NewPolicyRepository(fakePolicies{outcome: result}, rec)
	destinations := NewDestinationRepository(fakeDestinations{outcome: result}, rec)
	snapshots := NewSnapshotRepository(fakeSnapshots{outcome: result}, rec)
	users := NewUserRepository(fakeUsers{outcome: result}, rec)
	audit := NewAuditRepository(fakeAudit{outcome: result}, rec)

	types := []struct {
		changeType, topic string
		writes            []write
	}{
		{websocket.ChangeTypeAgents, websocket.ChangesTopic, []write{
			{name: "Create", call: func() error { return agents.Create(ctx, &db.Agent{Base: db.Base{ID: id}, Status: "online"}) }, id: id.String()},
			{name: "Update", call: func() error { return agents.Update(ctx, &db.Agent{Base: db.Base{ID: id}}) }, id: id.String()},
			{name: "Delete", call: func() error { return agents.Delete(ctx, id) }, id: id.String()},
			{name: "MarkOfflineIfStale", call: func() error { return ignore(agents.MarkOfflineIfStale(ctx, id, now)) }, id: id.String(), reports: true},
			{name: "BindCertFingerprint", call: func() error { return ignore(agents.BindCertFingerprint(ctx, id, "fp")) }, id: id.String(), reports: true},
			{name: "ResetCertFingerprint", call: func() error { return agents.ResetCertFingerprint(ctx, id) }, id: id.String()},
		}},
		{websocket.ChangeTypePolicies, websocket.ChangesTopic, []write{
			{name: "Create", call: func() error { return policies.Create(ctx, &db.Policy{Base: db.Base{ID: id}}) }, id: id.String()},
			{name: "Update", call: func() error { return policies.Update(ctx, &db.Policy{Base: db.Base{ID: id}}) }, id: id.String()},
			{name: "Delete", call: func() error { return policies.Delete(ctx, id) }, id: id.String()},
			{name: "UpdateSchedule", call: func() error { return policies.UpdateSchedule(ctx, id, now, now) }, id: id.String()},
			{name: "UpdateNextRunAt", call: func() error { return policies.UpdateNextRunAt(ctx, id, now) }, id: id.String()},
			{name: "CreateWithDestinations", call: func() error { return policies.CreateWithDestinations(ctx, &db.Policy{Base: db.Base{ID: id}}, nil) }, id: id.String()},
			{name: "UpdateWithDestinations", call: func() error { return policies.UpdateWithDestinations(ctx, &db.Policy{Base: db.Base{ID: id}}, nil) }, id: id.String()},
			{name: "AddDestination", call: func() error { return policies.AddDestination(ctx, &db.PolicyDestination{PolicyID: id}) }, id: id.String()},
			{name: "RemoveDestination", call: func() error { return policies.RemoveDestination(ctx, id, other) }, id: id.String()},
			{name: "DeleteDestinationAssociations", call: func() error { return policies.DeleteDestinationAssociations(ctx, other) }, id: ""},
		}},
		{websocket.ChangeTypeDestinations, websocket.ChangesTopic, []write{
			{name: "Create", call: func() error { return destinations.Create(ctx, &db.Destination{Base: db.Base{ID: id}}) }, id: id.String()},
			{name: "Update", call: func() error { return destinations.Update(ctx, &db.Destination{Base: db.Base{ID: id}}) }, id: id.String()},
			{name: "UpdateRepoSize", call: func() error { return destinations.UpdateRepoSize(ctx, id, 1, now) }, id: id.String()},
			{name: "MarkRepoInitialized", call: func() error { return destinations.MarkRepoInitialized(ctx, id, now) }, id: id.String()},
			{name: "ResetRepoInitialized", call: func() error { return destinations.ResetRepoInitialized(ctx, id) }, id: id.String()},
			{name: "Delete", call: func() error { return destinations.Delete(ctx, id) }, id: id.String()},
			{name: "UpdateLastCheck", call: func() error { return destinations.UpdateLastCheck(ctx, id, other, "succeeded", now) }, id: id.String()},
			{name: "TryAcquireBusyAll one", call: func() error { return ignore(destinations.TryAcquireBusyAll(ctx, []uuid.UUID{id}, other, "backup")) }, id: id.String(), reports: true},
			{name: "TryAcquireBusyAll several", call: func() error {
				return ignore(destinations.TryAcquireBusyAll(ctx, []uuid.UUID{id, other}, other, "backup"))
			}, id: "", reports: true},
			{name: "ReleaseBusy", call: func() error { return destinations.ReleaseBusy(ctx, id, other) }, id: id.String()},
			{name: "ReleaseBusyForJobs", call: func() error { return destinations.ReleaseBusyForJobs(ctx, []uuid.UUID{other}) }, id: ""},
			{name: "ReleaseStaleBusy", call: func() error { return ignore(destinations.ReleaseStaleBusy(ctx)) }, id: "", reports: true},
			{name: "StartMaintenance", call: func() error { return destinations.StartMaintenance(ctx, id, "admin") }, id: id.String()},
			{name: "EndMaintenance", call: func() error { return destinations.EndMaintenance(ctx, id) }, id: id.String()},
			{name: "ClaimForRepair", call: func() error { return ignore(destinations.ClaimForRepair(ctx, id, other, "admin")) }, id: id.String()},
			{name: "ClearEndedMaintenance", call: func() error { return ignore(destinations.ClearEndedMaintenance(ctx)) }, id: "", reports: true},
		}},
		{websocket.ChangeTypeSnapshots, websocket.ChangesTopic, []write{
			{name: "Create", call: func() error { return snapshots.Create(ctx, &db.Snapshot{Base: db.Base{ID: id}}) }, id: id.String()},
			{name: "Delete", call: func() error { return snapshots.Delete(ctx, id) }, id: id.String()},
			{name: "DeleteStaleByDestination", call: func() error { return ignore(snapshots.DeleteStaleByDestination(ctx, other, nil, now)) }, id: "", reports: true},
			{name: "FillMissingSummary", call: func() error { return ignore(snapshots.FillMissingSummary(ctx, other, "abc", 1, db.SnapshotSummary{})) }, id: "", reports: true},
		}},
		{websocket.ChangeTypeUsers, websocket.AdminChangesTopic, []write{
			{name: "Create", call: func() error { return users.Create(ctx, &db.User{Base: db.Base{ID: id}}) }, id: id.String()},
			{name: "Update", call: func() error { return users.Update(ctx, &db.User{Base: db.Base{ID: id}}) }, id: id.String()},
			{name: "Delete", call: func() error { return users.Delete(ctx, id) }, id: id.String()},
		}},
		{websocket.ChangeTypeAudit, websocket.AdminChangesTopic, []write{
			{name: "Create", call: func() error { return audit.Create(ctx, &db.AuditLog{ID: id}) }, id: id.String()},
		}},
	}

	for _, typ := range types {
		for _, w := range typ.writes {
			t.Run(typ.changeType+"/"+w.name, func(t *testing.T) {
				*result = outcome{done: true}
				if err := w.call(); err != nil {
					t.Fatal(err)
				}
				want := published{typ.topic, typ.changeType, w.id}
				if got := rec.takeChanges(t); len(got) != 1 || got[0] != want {
					t.Errorf("published %v, want [%v]", got, want)
				}

				*result = outcome{err: errors.New("db down"), done: true}
				if err := w.call(); err == nil {
					t.Fatal("the write's error was lost")
				}
				if got := rec.takeChanges(t); len(got) != 0 {
					t.Errorf("a failed write published %v", got)
				}

				if w.reports {
					*result = outcome{}
					_ = w.call()
					if got := rec.takeChanges(t); len(got) != 0 {
						t.Errorf("a write that changed nothing published %v", got)
					}
				}
			})
		}
	}
}

// TestAgentRepository_StatusChangesOnly: heartbeats rewrite the status every
// 30 seconds; only a new status is announced.
func TestAgentRepository_StatusChangesOnly(t *testing.T) {
	ctx := context.Background()
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	rec := &recorder{}
	result := &outcome{}
	agents := NewAgentRepository(fakeAgents{outcome: result}, rec)
	now := time.Now()

	steps := []struct {
		status  string
		publish bool
	}{
		{"online", true}, // first status known to this server
		{"online", false},
		{"online", false},
		{"offline", true},
		{"online", true},
	}
	for i, step := range steps {
		if err := agents.UpdateStatus(ctx, id, step.status, now); err != nil {
			t.Fatal(err)
		}
		got := rec.takeChanges(t)
		if step.publish != (len(got) == 1) || len(got) > 1 {
			t.Errorf("step %d (%s): published %v, want a change: %v", i, step.status, got, step.publish)
		}
	}

	// The watchdog marking it offline counts as a status written.
	result.done = true
	if _, err := agents.MarkOfflineIfStale(ctx, id, now); err != nil {
		t.Fatal(err)
	}
	rec.takeChanges(t)
	if err := agents.UpdateStatus(ctx, id, "offline", now); err != nil {
		t.Fatal(err)
	}
	if got := rec.takeChanges(t); len(got) != 0 {
		t.Errorf("offline after the watchdog's offline published %v", got)
	}
}
