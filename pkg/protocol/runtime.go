package protocol

import (
	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/command"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
)

// Conn is the byte-oriented connection required by ACP. Concrete framing
// implementations live in pkg/transport.
type Conn interface {
	ReadMessage() ([]byte, error)
	WriteMessage([]byte) error
	Close() error
}

// Runtime is the application capability surface used by ACP. The protocol
// layer depends on operations, not on the application's internal state.
type Runtime interface {
	Commands() *command.Registry
	Skills() *skill.LoadResult
	SessionID() string
	CurrentWorkdir() string
	ContextWindow() int
	ModelName() (string, string)
	Agent() *agent.Agent
	HandlePrompt(string, bool, string) (any, error)
	RegisterAllHandlers()
	BuildSkillCommands()
	InitEventEmitter(func(agent.AgentEvent)) (chan struct{}, chan struct{})
	StartDebugServer()
	SetSession(*session.Session, string, string)
	LoadSession(string) (*session.Session, string, error)
	ModelCatalog() ([]config.ModelSpec, config.ModelInfo, error)
	ResolveModelOption(string) (string, string, error)
	SetModel(string, string) error
	FormatCommandResult(string, any) string
}
