package protocol

import (
	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/command"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
)

type testRuntime struct {
	commands      *command.Registry
	skills        *skill.LoadResult
	sessionID     string
	cwd           string
	contextWindow int
	provider      string
	model         string
	emit          func(agent.AgentEvent)
}

func (r *testRuntime) Commands() *command.Registry                    { return r.commands }
func (r *testRuntime) Skills() *skill.LoadResult                      { return r.skills }
func (r *testRuntime) SessionID() string                              { return r.sessionID }
func (r *testRuntime) CurrentWorkdir() string                         { return r.cwd }
func (r *testRuntime) ContextWindow() int                             { return r.contextWindow }
func (r *testRuntime) ModelName() (string, string)                    { return r.provider, r.model }
func (r *testRuntime) Agent() *agent.Agent                            { return nil }
func (r *testRuntime) HandlePrompt(string, bool, string) (any, error) { return nil, nil }
func (r *testRuntime) RegisterAllHandlers()                           {}
func (r *testRuntime) BuildSkillCommands()                            {}
func (r *testRuntime) InitEventEmitter(emit func(agent.AgentEvent)) (chan struct{}, chan struct{}) {
	r.emit = emit
	shutdown := make(chan struct{})
	done := make(chan struct{})
	go func() { <-shutdown; close(done) }()
	return shutdown, done
}
func (r *testRuntime) StartDebugServer()                                    {}
func (r *testRuntime) SetSession(*session.Session, string, string)          {}
func (r *testRuntime) LoadSession(string) (*session.Session, string, error) { return nil, "", nil }
func (r *testRuntime) ModelCatalog() ([]config.ModelSpec, config.ModelInfo, error) {
	return nil, config.ModelInfo{}, nil
}
func (r *testRuntime) ResolveModelOption(string) (string, string, error) { return "", "", nil }
func (r *testRuntime) SetModel(string, string) error                     { return nil }
func (r *testRuntime) FormatCommandResult(string, any) string            { return "" }
