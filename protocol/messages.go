package protocol

const (
	MsgHello        = "HELLO"
	MsgAuthOK       = "AUTH_OK"
	MsgHeartbeat    = "HEARTBEAT"
	MsgHeartbeatAck = "HEARTBEAT_ACK"
	MsgExec         = "EXEC"
	MsgExecResult   = "EXEC_RESULT"
)

type Message struct {
	Type     string            `json:"type"`
	DeviceID string            `json:"device_id,omitempty"`
	Version  string            `json:"version,omitempty"`
	
	// Phase 3 fields
	RequestID string            `json:"request_id,omitempty"`
	Operation string            `json:"operation,omitempty"`
	Args      map[string]string `json:"args,omitempty"`
	Success   bool              `json:"success,omitempty"`
	Result    map[string]string `json:"result,omitempty"`
	Error     string            `json:"error,omitempty"`
}
