package changes

import (
	"sync"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// CoalesceWindow is how long changes of a type are gathered after one was
// published. The GUI refreshes at most once per second anyway, and a backup
// with several destinations writes its job many times in a burst: without
// it, a burst could fill a client's send buffer and get it disconnected.
const CoalesceWindow = time.Second

// coalescer is a Publisher that publishes the first change of a type at once,
// then gathers the changes of that type for a window and publishes them as
// one at its end: with their ID if they all concern one object, without one
// otherwise. Other messages go straight through.
type coalescer struct {
	pub    Publisher
	window time.Duration
	// afterFunc runs f after d; time.AfterFunc, replaced in tests.
	afterFunc func(d time.Duration, f func())

	mu sync.Mutex
	// open holds the types whose window is running, with the change gathered
	// in it (nil while none).
	open map[string]*websocket.Change
}

// Coalesce returns a Publisher that coalesces the changes published through
// it on pub, per type, over window.
func Coalesce(pub Publisher, window time.Duration) Publisher {
	return &coalescer{
		pub:       pub,
		window:    window,
		afterFunc: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		open:      make(map[string]*websocket.Change),
	}
}

func (c *coalescer) Publish(topic string, msg websocket.Message) {
	change, ok := msg.Payload.(websocket.Change)
	if topic != websocket.ChangesTopic || msg.Type != websocket.MsgChange || !ok {
		c.pub.Publish(topic, msg)
		return
	}

	c.mu.Lock()
	gathered, running := c.open[change.Type]
	switch {
	case !running:
		c.open[change.Type] = nil
	case gathered == nil:
		c.open[change.Type] = &change
	case gathered.ID != change.ID:
		gathered.ID = ""
	}
	c.mu.Unlock()

	if !running {
		c.pub.Publish(topic, msg)
		c.afterFunc(c.window, func() { c.flush(change.Type) })
	}
}

// flush ends the window of a type: it publishes the change gathered in it and
// opens a new window, or closes it when nothing was gathered.
func (c *coalescer) flush(changeType string) {
	c.mu.Lock()
	gathered := c.open[changeType]
	if gathered == nil {
		delete(c.open, changeType)
	} else {
		c.open[changeType] = nil
	}
	c.mu.Unlock()

	if gathered != nil {
		c.pub.Publish(websocket.ChangesTopic, websocket.Message{Type: websocket.MsgChange, Payload: *gathered})
		c.afterFunc(c.window, func() { c.flush(changeType) })
	}
}
