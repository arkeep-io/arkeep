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
	open map[windowKey]*websocket.Change
}

// windowKey identifies a window: changes are gathered per topic and type.
type windowKey struct{ topic, changeType string }

// Coalesce returns a Publisher that coalesces the changes published through
// it on pub, per type, over window.
func Coalesce(pub Publisher, window time.Duration) Publisher {
	return &coalescer{
		pub:       pub,
		window:    window,
		afterFunc: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		open:      make(map[windowKey]*websocket.Change),
	}
}

func (c *coalescer) Publish(topic string, msg websocket.Message) {
	change, ok := msg.Payload.(websocket.Change)
	if msg.Type != websocket.MsgChange || !ok {
		c.pub.Publish(topic, msg)
		return
	}
	key := windowKey{topic, change.Type}

	c.mu.Lock()
	gathered, running := c.open[key]
	switch {
	case !running:
		c.open[key] = nil
	case gathered == nil:
		c.open[key] = &change
	case gathered.ID != change.ID:
		gathered.ID = ""
	}
	c.mu.Unlock()

	if !running {
		c.pub.Publish(topic, msg)
		c.afterFunc(c.window, func() { c.flush(key) })
	}
}

// flush ends a window: it publishes the change gathered in it and opens a new
// window, or closes it when nothing was gathered.
func (c *coalescer) flush(key windowKey) {
	c.mu.Lock()
	gathered := c.open[key]
	if gathered == nil {
		delete(c.open, key)
	} else {
		c.open[key] = nil
	}
	c.mu.Unlock()

	if gathered != nil {
		c.pub.Publish(key.topic, websocket.Message{Type: websocket.MsgChange, Payload: *gathered})
		c.afterFunc(c.window, func() { c.flush(key) })
	}
}
