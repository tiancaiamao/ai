package middlewares

import (
	"regexp"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

const middlewareName = "destructive_guard"

// defaultProtectedPatterns are regex patterns that match commonly destructive
// shell commands. These are matched against bash tool output text.
var defaultProtectedPatterns = []string{
	`rm\s+-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*`, // rm -rf, rm -fr, rm -Rf, etc.
	`rm\s+-[a-zA-Z]*f[a-zA-Z]*r[a-zA-Z]*`, // rm -fr variant
	`rm\s+-r\s`,                           // rm -r
	`kill\s+-9`,                           // kill -9
	`mkfs`,                                // mkfs
	`dd\s+if=`,                            // dd if=
	`\:\(\)\{\s*\:\|\:\&\s*\}`,            // fork bomb
}

// destructiveGuard implements AfterToolHook to detect destructive commands
// in bash tool output and append warnings.
type destructiveGuard struct {
	patterns []*regexp.Regexp
}

// newDestructiveGuard creates a guard with the given patterns.
// If patterns is empty, defaultProtectedPatterns are used.
func newDestructiveGuard(patterns []string) (*destructiveGuard, error) {
	if len(patterns) == 0 {
		patterns = defaultProtectedPatterns
	}
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		r, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, r)
	}
	return &destructiveGuard{patterns: compiled}, nil
}

const warningText = "\n\n⚠️ WARNING: Destructive command detected in output. Please verify before proceeding."

// afterTool is the AfterToolHook implementation.
func (g *destructiveGuard) afterTool(hctx agent.HookContext, toolName string, result agentctx.AgentMessage) (agentctx.AgentMessage, error) {
	// Only inspect bash tool results.
	if toolName != "bash" {
		return result, nil
	}

	// Scan text content blocks for destructive patterns.
	matched := false
	for _, block := range result.Content {
		tc, ok := block.(agentctx.TextContent)
		if !ok {
			continue
		}
		if g.matches(tc.Text) {
			matched = true
			break
		}
	}

	if !matched {
		return result, nil
	}

	// Append warning as a new text block; keep original content intact.
	ensureContentSlice(&result)
	result.Content = append(result.Content, agentctx.TextContent{
		Type: "text",
		Text: warningText,
	})
	return result, nil
}

// matches checks if any compiled pattern matches the input text.
func (g *destructiveGuard) matches(text string) bool {
	for _, p := range g.patterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}

// extractStringSlice attempts to extract a []string from params[key].
func extractStringSlice(params map[string]any, key string) []string {
	raw, ok := params[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}

// newDestructiveGuardFromParams is the AfterToolFactory for the destructive guard.
// It supports an optional "protected_patterns" key in params (a list of regex strings).
// When absent or empty, the default patterns are used.
func newDestructiveGuardFromParams(params map[string]any) (agent.AfterToolHook, error) {
	patterns := extractStringSlice(params, "protected_patterns")
	guard, err := newDestructiveGuard(patterns)
	if err != nil {
		return nil, err
	}
	return guard.afterTool, nil
}

func init() {
	Register(MiddlewareSpec{
		Name:      middlewareName,
		AfterTool: newDestructiveGuardFromParams,
	})
}

// ensure defaultProtectedPatterns strings compile at init time.
func init() {
	for _, p := range defaultProtectedPatterns {
		regexp.MustCompile(p)
	}
}
