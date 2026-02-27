// Package websocket implements the real-time pub/sub hub that pushes server
// events to connected GUI clients. It uses gorilla/websocket under the hood
// and exposes a topic-based broadcast API consumed by the scheduler, gRPC
// handlers, and notification service.
//
// Topic naming convention:
//
//	job:<uuid>                — status updates for a specific backup job
//	agent:<uuid>             — online/offline/error transitions for an agent
//	notifications:<user_id>  — in-app notifications for a specific user
package websocket

// MessageType identifies the kind of event carried by a Message.
// The GUI uses this field to route the payload to the correct store update.
type MessageType string

const (
	// MsgJobStatus is sent when a job transitions between states
	// (pending → running → succeeded | failed).
	MsgJobStatus MessageType = "job.status"

	// MsgJobLog is sent for each streamed log line during an active backup.
	MsgJobLog MessageType = "job.log"

	// MsgAgentStatus is sent when an agent connects, disconnects, or errors.
	MsgAgentStatus MessageType = "agent.status"

	// MsgAgentMetrics is sent on every agent heartbeat with a snapshot of
	// current host resource utilization (CPU, memory, disk percentages).
	// Published on the "agent:<uuid>" topic so the detail page can display
	// live gauges without polling the REST API.
	MsgAgentMetrics MessageType = "agent.metrics"

	// MsgNotification is sent when a new in-app notification is created for
	// the subscribed user.
	MsgNotification MessageType = "notification"

	// MsgPing is sent by the hub periodically to keep the connection alive
	// and let the client detect stale connections.
	MsgPing MessageType = "ping"
)

// Message is the envelope for every WebSocket frame sent to clients.
// The GUI deserializes this struct and dispatches on Type.
//
// JSON example:
//
//	{"type":"job.status","topic":"job:018f...","payload":{"status":"running"}}
type Message struct {
	// Type identifies the kind of event so the client can route it correctly.
	Type MessageType `json:"type"`

	// Topic is the pub/sub channel this message was published on.
	// Clients use it to associate the update with the correct UI element.
	Topic string `json:"topic"`

	// Payload carries the event-specific data. The shape varies by Type:
	//   - job.status:    {"status":"running","started_at":"..."}
	//   - job.log:       {"level":"info","message":"...","timestamp":"..."}
	//   - agent.status:  {"status":"online","ip_address":"..."}
	//   - agent.metrics: {"cpu_percent":12.5,"mem_percent":60.1,"disk_percent":45.0}
	//   - notification:  {"id":"...","type":"...","title":"...","body":"..."}
	//   - ping:          {} (empty)
	Payload any `json:"payload"`
}