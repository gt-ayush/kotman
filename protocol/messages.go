package protocol

// Message types
const (
	MsgHello        = "HELLO"
	MsgAuthOK       = "AUTH_OK"
	MsgHeartbeat    = "HEARTBEAT"
	MsgHeartbeatAck = "HEARTBEAT_ACK"
)

// Message is the standard envelope for Phase 1 communication
type Message struct {
	Type     string `json:"type"`
	DeviceID string `json:"device_id,omitempty"`
	Version  string `json:"version,omitempty"`
}
