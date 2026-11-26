package changes

import (
	"github.com/google/uuid"

	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// notifier publishes the changes of one object type on its topic
// (websocket.ChangeTopic). Every decorator embeds one.
type notifier struct {
	pub        Publisher
	changeType string
}

func newNotifier(pub Publisher, changeType string) notifier {
	return notifier{pub: pub, changeType: changeType}
}

// changed publishes a change of the object id, or of several objects when id
// is the nil UUID.
func (n notifier) changed(id uuid.UUID) {
	change := websocket.Change{Type: n.changeType}
	if id != uuid.Nil {
		change.ID = id.String()
	}
	n.pub.Publish(websocket.ChangeTopic(n.changeType), websocket.Message{Type: websocket.MsgChange, Payload: change})
}

// after publishes a change of id when err is nil, and returns err.
func (n notifier) after(id uuid.UUID, err error) error {
	if err == nil {
		n.changed(id)
	}
	return err
}

// afterIf publishes a change of id when the write succeeded and changed
// something (done), and returns its results.
func (n notifier) afterIf(id uuid.UUID, done bool, err error) (bool, error) {
	if err == nil && done {
		n.changed(id)
	}
	return done, err
}

// afterBulk publishes a change of several objects when the write succeeded
// and changed at least one row.
func (n notifier) afterBulk(rows int64, err error) (int64, error) {
	if err == nil && rows > 0 {
		n.changed(uuid.Nil)
	}
	return rows, err
}

// afterMany publishes a change of the objects ids when err is nil: with its ID
// for one object, without one for several.
func (n notifier) afterMany(ids []uuid.UUID, err error) error {
	if err == nil && len(ids) > 0 {
		if len(ids) == 1 {
			n.changed(ids[0])
		} else {
			n.changed(uuid.Nil)
		}
	}
	return err
}
