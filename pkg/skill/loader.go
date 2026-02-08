package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Loader handles loading skills from directories.
type Loader struct {
	agentDir string // Agent directory (default: ~/.ai)
}

// NewLoader creates a new skill loader.
func NewLoader(agentDir string) *Loader {
	return &Loader{
		agentDir: agentDir,
	}
}

// LoadOptions contains options for loading skills.
type LoadOptions struct {
	// Working directory for project-local skills (default: current directory)
	CWD string
	// Agent config directory for global skills (default: ~/.ai)
	AgentDir string
	// Explicit skill paths (files or directories)
	SkillPaths []string
	// Include default skills directories (default: true)
	IncludeDefaults bool
}

// Load loads skills from all configured locations.
func (l *Loader) Load(opts *LoadOptions) *LoadResult {
	if opts == nil {
		opts = &LoadOptions{}
	}

	// Set defaults
	if opts.CWD == "" {
		opts.CWD = mustGetwd()
	}
	if opts.AgentDir == "" {
		opts.AgentDir = l.agentDir
	}
	if opts.IncludeDefaults == false {
		opts.IncludeDefaults = true
	}

	skillMap := make(map[string]Skill)
	realPathSet := make(map[string]bool)
	allDiagnostics := []Diagnostic{}
	collisionDiagnostics := []Diagnostic{}

	// Helper function to add skills
	addSkills := func(result *LoadResult) {
		allDiagnostics = append(allDiagnostics, result.Diagnostics...)
		for _, skill := range result.Skills {
			// Resolve symlinks to detect duplicate files
			realPath, err := filepath.EvalSymlinks(skill.FilePath)
			if err != nil {
				realPath = skill.FilePath
			}

			// Skip silently if we've already loaded this exact file (via symlink)
			if realPathSet[realPath] {
				continue
			}

			// Check for name collision
			if existing, ok := skillMap[skill.Name]; ok {
				collisionDiagnostics = append(collisionDiagnostics, Diagnostic{
					Type:    "collision",
					Message: fmt.Sprintf("name %q collision", skill.Name),
					Path:    skill.FilePath,
					Collision: &CollisionInfo{
						ResourceType: "skill",
						Name:         skill.Name,
						WinnerPath:   existing.FilePath,
						LoserPath:    skill.FilePath,
					},
				})
			} else {
				skillMap[skill.Name] = skill
				realPathSet[realPath] = true
			}
		}
	}

	// Load from default directories
	if opts.IncludeDefaults {
		// User skills: ~/.ai/skills/
		userSkillsDir := filepath.Join(opts.AgentDir, "skills")
		addSkills(l.loadFromDir(userSkillsDir, "user", true))

		// Project skills: .ai/skills/
		projectSkillsDir := filepath.Join(opts.CWD, ".ai", "skills")
		addSkills(l.loadFromDir(projectSkillsDir, "project", true))
	}

	// Load from explicit paths
	for _, rawPath := range opts.SkillPaths {
		resolvedPath := l.resolvePath(rawPath, opts.CWD)
		if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
			allDiagnostics = append(allDiagnostics, Diagnostic{
				Type:    "warning",
				Message: "skill path does not exist",
				Path:    resolvedPath,
			})
			continue
		}

		info, err := os.Stat(resolvedPath)
		if err != nil {
			allDiagnostics = append(allDiagnostics, Diagnostic{
				Type:    "warning",
				Message: fmt.Sprintf("failed to read skill path: %v", err),
				Path:    resolvedPath,
			})
			continue
		}

		source := l.getSource(resolvedPath, opts)
		if info.IsDir() {
			addSkills(l.loadFromDir(resolvedPath, source, true))
		} else if strings.HasSuffix(resolvedPath, ".md") {
			result := l.loadFromFile(resolvedPath, source)
			if result.Skill != nil {
				addSkills(&LoadResult{Skills: []Skill{*result.Skill}, Diagnostics: result.Diagnostics})
			} else {
				allDiagnostics = append(allDiagnostics, result.Diagnostics...)
			}
		} else {
			allDiagnostics = append(allDiagnostics, Diagnostic{
				Type:    "warning",
				Message: "skill path is not a markdown file",
				Path:    resolvedPath,
			})
		}
	}

	// Convert map to slice
	skills := make([]Skill, 0, len(skillMap))
	for _, skill := range skillMap {
		skills = append(skills, skill)
	}

	return &LoadResult{
		Skills:      skills,
		Diagnostics: append(allDiagnostics, collisionDiagnostics...),
	}
}

// loadFromDir loads skills from a directory.
// Discovery rules:
// - Direct .md children in the root (when includeRootFiles=true)
// - Recursive SKILL.md under subdirectories
func (l *Loader) loadFromDir(dir string, source string, includeRootFiles bool) *LoadResult {
	skills := []Skill{}
	diagnostics := []Diagnostic{}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return &LoadResult{Skills: skills, Diagnostics: diagnostics}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return &LoadResult{Skills: skills, Diagnostics: diagnostics}
	}

	for _, entry := range entries {
		// Skip hidden entries and node_modules
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.Name() == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())

		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Follow symlinks
		if entry.Type()&os.ModeSymlink != 0 {
			info, err = os.Stat(fullPath)
			if err != nil {
				continue
			}
		}

		if info.IsDir() {
			// Recursively load from subdirectories
			subResult := l.loadFromDir(fullPath, source, false)
			skills = append(skills, subResult.Skills...)
			diagnostics = append(diagnostics, subResult.Diagnostics...)
			continue
		}

		if !info.Mode().IsRegular() {
			continue
		}

		// Check if this is a skill file
		isRootMd := includeRootFiles && strings.HasSuffix(entry.Name(), ".md")
		isSkillMd := !includeRootFiles && entry.Name() == "SKILL.md"

		if !isRootMd && !isSkillMd {
			continue
		}

		result := l.loadFromFile(fullPath, source)
		if result.Skill != nil {
			skills = append(skills, *result.Skill)
		}
		diagnostics = append(diagnostics, result.Diagnostics...)
	}

	return &LoadResult{Skills: skills, Diagnostics: diagnostics}
}

type fileLoadResult struct {
	Skill       *Skill
	Diagnostics []Diagnostic
}

// loadFromFile loads a single skill from a file.
func (l *Loader) loadFromFile(filePath string, source string) *fileLoadResult {
	diagnostics := []Diagnostic{}

	// Read file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return &fileLoadResult{
			Diagnostics: []Diagnostic{{
				Type:    "error",
				Message: fmt.Sprintf("failed to read file: %v", err),
				Path:    filePath,
			}},
		}
	}

	// Parse frontmatter and content
	frontmatter, bodyContent, err := parseFrontmatter(content)
	if err != nil {
		diagnostics = append(diagnostics, Diagnostic{
			Type:    "warning",
			Message: fmt.Sprintf("failed to parse frontmatter: %v", err),
			Path:    filePath,
		})
	}

	// Validate frontmatter fields
	for key := range frontmatter.Metadata {
		if !allowedFrontmatterFields[key] && key != "name" && key != "description" {
			diagnostics = append(diagnostics, Diagnostic{
				Type:    "warning",
				Message: fmt.Sprintf("unknown frontmatter field %q", key),
				Path:    filePath,
			})
		}
	}

	// Extract skill directory and parent directory name
	skillDir := filepath.Dir(filePath)
	parentDirName := filepath.Base(skillDir)

	// Use name from frontmatter, or fall back to parent directory name
	name := frontmatter.Name
	if name == "" {
		name = parentDirName
	}

	// Validate name
	for _, err := range validateName(name, parentDirName) {
		diagnostics = append(diagnostics, Diagnostic{
			Type:    "warning",
			Message: err,
			Path:    filePath,
		})
	}

	// Validate description
	for _, err := range validateDescription(frontmatter.Description) {
		diagnostics = append(diagnostics, Diagnostic{
			Type:    "warning",
			Message: err,
			Path:    filePath,
		})
	}

	// Don't load if description is completely missing
	if frontmatter.Description == "" {
		return &fileLoadResult{Diagnostics: diagnostics}
	}

	skill := &Skill{
		Name:                   name,
		Description:            frontmatter.Description,
		FilePath:               filePath,
		BaseDir:                skillDir,
		Source:                 source,
		Content:                string(bodyContent),
		Frontmatter:            *frontmatter,
		DisableModelInvocation: frontmatter.DisableModelInvocation,
	}

	return &fileLoadResult{Skill: skill, Diagnostics: diagnostics}
}

// resolvePath resolves a path relative to cwd, supporting ~ expansion.
func (l *Loader) resolvePath(p string, cwd string) string {
	trimmed := strings.TrimSpace(p)

	// Expand ~
	if trimmed == "~" {
		homeDir, _ := os.UserHomeDir()
		return homeDir
	}
	if strings.HasPrefix(trimmed, "~/") {
		homeDir, _ := os.UserHomeDir()
		return filepath.Join(homeDir, trimmed[2:])
	}
	if strings.HasPrefix(trimmed, "~") {
		homeDir, _ := os.UserHomeDir()
		return filepath.Join(homeDir, trimmed[1:])
	}

	// If absolute, return as-is
	if filepath.IsAbs(trimmed) {
		return trimmed
	}

	// Otherwise, resolve relative to cwd
	return filepath.Join(cwd, trimmed)
}

// getSource determines the source type for a resolved path.
func (l *Loader) getSource(resolvedPath string, opts *LoadOptions) string {
	if !opts.IncludeDefaults {
		userSkillsDir := filepath.Join(opts.AgentDir, "skills")
		projectSkillsDir := filepath.Join(opts.CWD, ".ai", "skills")

		if isUnderPath(resolvedPath, userSkillsDir) {
			return "user"
		}
		if isUnderPath(resolvedPath, projectSkillsDir) {
			return "project"
		}
	}
	return "path"
}

// isUnderPath checks if target is under root directory.
func isUnderPath(target, root string) bool {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}

	if absTarget == absRoot {
		return true
	}

	// Ensure root ends with separator
	if !strings.HasSuffix(absRoot, string(filepath.Separator)) {
		absRoot += string(filepath.Separator)
	}

	return strings.HasPrefix(absTarget, absRoot)
}

// mustGetwd returns the current working directory or panics.
func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return wd
}
