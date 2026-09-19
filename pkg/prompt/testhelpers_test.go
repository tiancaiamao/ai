package prompt

// newBuilder is a test-only constructor with a fixed cwd and no workspace.
// Production code uses NewBuilderWithWorkspace.
func newBuilder(_, cwd string) *Builder {
	return &Builder{
		cwd:     cwd,
		minimal: false,
	}
}
