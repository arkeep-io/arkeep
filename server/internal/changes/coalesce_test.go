package changes

import (
	"slices"
	"testing"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// manualTimers stands in for time.AfterFunc: windows end when the test says.
type manualTimers struct{ pending []func() }

func (m *manualTimers) after(_ time.Duration, f func()) { m.pending = append(m.pending, f) }

// fire ends every window running now.
func (m *manualTimers) fire() {
	due := m.pending
	m.pending = nil
	for _, f := range due {
		f()
	}
}

func newCoalescer(t *testing.T) (*coalescer, *recorder, *manualTimers) {
	t.Helper()
	rec := &recorder{}
	timers := &manualTimers{}
	c := Coalesce(rec, time.Second).(*coalescer)
	c.afterFunc = timers.after
	return c, rec, timers
}

func publishChange(c Publisher, changeType, id string) {
	c.Publish(websocket.ChangesTopic, websocket.Message{
		Type:    websocket.MsgChange,
		Payload: websocket.Change{Type: changeType, ID: id},
	})
}

func TestCoalesce(t *testing.T) {
	tests := []struct {
		name string
		// during are the IDs of the changes published while the first window
		// runs, after the first change "a".
		during []string
		// want are the IDs published: "a" at once, then the gathered one.
		want []string
	}{
		{"alone", nil, []string{"a"}},
		{"same object", []string{"a", "a"}, []string{"a", "a"}},
		{"one other object", []string{"b", "b"}, []string{"a", "b"}},
		{"several objects", []string{"b", "c"}, []string{"a", ""}},
		{"a bulk change", []string{"b", ""}, []string{"a", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec, timers := newCoalescer(t)
			publishChange(c, websocket.ChangeTypeJobs, "a")
			if got := rec.take(t); !slices.Equal(got, []string{"a"}) {
				t.Fatalf("first change published %q, want it at once", got)
			}
			for _, id := range tt.during {
				publishChange(c, websocket.ChangeTypeJobs, id)
			}
			if got := rec.take(t); len(got) != 0 {
				t.Fatalf("changes during the window published %q at once", got)
			}
			timers.fire()
			timers.fire() // the window opened by the flush ends empty
			if got := rec.take(t); !slices.Equal(got, tt.want[1:]) {
				t.Errorf("published %q at the end of the window, want %q", got, tt.want[1:])
			}
			if len(timers.pending) != 0 || len(c.open) != 0 {
				t.Errorf("a window is still running after an empty one")
			}
		})
	}
}

// TestCoalesce_ReopensAfterQuietWindow: once a window ended empty, the next
// change is published at once again.
func TestCoalesce_ReopensAfterQuietWindow(t *testing.T) {
	c, rec, timers := newCoalescer(t)
	publishChange(c, websocket.ChangeTypeJobs, "a")
	timers.fire()
	rec.take(t)

	publishChange(c, websocket.ChangeTypeJobs, "b")
	if got := rec.take(t); !slices.Equal(got, []string{"b"}) {
		t.Errorf("published %q, want b at once", got)
	}
}

// TestCoalesce_PassesOtherMessages: messages that are not changes are never
// held back.
func TestCoalesce_PassesOtherMessages(t *testing.T) {
	c, rec, timers := newCoalescer(t)
	msg := websocket.Message{Type: websocket.MsgJobStatus, Payload: map[string]string{"status": "running"}}
	c.Publish("job:1", msg)
	c.Publish("job:1", msg)

	if len(rec.sent) != 2 || rec.tops[0] != "job:1" || len(timers.pending) != 0 {
		t.Errorf("published %d messages on %q with %d windows, want 2 on job:1 and none", len(rec.sent), rec.tops, len(timers.pending))
	}
}
