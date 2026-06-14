package run

// EventKind classifies the type of formatted output.
type EventKind string

const (
	KindText          EventKind = "text"
	KindThinking      EventKind = "thinking"
	KindTool          EventKind = "tool"
	KindMeta          EventKind = "meta"
	KindSessionSwitch EventKind = "session_switch"
	KindResponse      EventKind = "response" // slash command response
)

// FormattedEvent is the result of parsing a raw JSON event line.
type FormattedEvent struct {
	Kind   EventKind
	Role   string // role prefix: "assistant", "thinking", "tool", "ai" for system messages
	Text   string // human-readable line (already formatted)
	Raw    string // original raw delta text (for stream.log append)
	Tool   string // tool name (KindTool only)
	Detail string // tool detail (KindTool only)
}
