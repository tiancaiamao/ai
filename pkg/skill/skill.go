package skill

import (
	"time"
)

// Skill represents a loaded agent skill.
type Skill struct {
	Name                   string      // Skill name (e.g., "react-component")
	Description            string      // Skill description
	FilePath               string      // Path to the skill markdown file
	BaseDir                string      // Directory containing the skill file
	Source                 string      // Source identifier: "user", "project", or "path"
	Content                string      // Full markdown content
	Frontmatter            Frontmatter // Parsed frontmatter
	DisableModelInvocation bool        // If true, skill won't be included in auto-prompt
	LoadedAt               time.Time   // When the skill was loaded
}

// Frontmatter represents the YAML frontmatter of a skill file.
type Frontmatter struct {
	Name                   string                 `yaml:"name"`
	Description            string                 `yaml:"description"`
	License                string                 `yaml:"license,omitempty"`
	Compatibility          string                 `yaml:"compatibility,omitempty"`
	Metadata               map[string]interface{} `yaml:"metadata,omitempty"`
	AllowedTools           []string               `yaml:"allowed-tools,omitempty"`
	DisableModelInvocation bool                   `yaml:"disable-model-invocation,omitempty"`
}

// LoadResult contains the result of loading skills.
type LoadResult struct {
	Skills      []Skill
	Diagnostics []Diagnostic
}

// Diagnostic represents a validation warning or error.
type Diagnostic struct {
	Type      string         `json:"type"` // "warning", "error", "collision"
	Message   string         `json:"message"`
	Path      string         `json:"path,omitempty"`
	Collision *CollisionInfo `json:"collision,omitempty"`
}

// CollisionInfo represents a skill name collision.
type CollisionInfo struct {
	ResourceType string `json:"resourceType"` // "skill"
	Name         string `json:"name"`
	WinnerPath   string `json:"winnerPath"` // First loaded skill
	LoserPath    string `json:"loserPath"`  // Later skill with same name
}

// Constants from Agent Skills spec
const (
	MaxNameLength        = 64
	MaxDescriptionLength = 1024

	// Allowed frontmatter fields per spec
	FieldName                   = "name"
	FieldDescription            = "description"
	FieldLicense                = "license"
	FieldCompatibility          = "compatibility"
	FieldMetadata               = "metadata"
	FieldAllowedTools           = "allowed-tools"
	FieldDisableModelInvocation = "disable-model-invocation"
)

var allowedFrontmatterFields = map[string]bool{
	FieldName:                   true,
	FieldDescription:            true,
	FieldLicense:                true,
	FieldCompatibility:          true,
	FieldMetadata:               true,
	FieldAllowedTools:           true,
	FieldDisableModelInvocation: true,
}
