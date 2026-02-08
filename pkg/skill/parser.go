package skill

import (
	"fmt"
	"regexp"
	"strings"
)

// parseFrontmatter parses YAML frontmatter from markdown content.
// Returns (frontmatter, body content, error).
func parseFrontmatter(content []byte) (*Frontmatter, []byte, error) {
	// Convert to string for easier manipulation
	str := string(content)

	// Check if content starts with ---
	if !strings.HasPrefix(str, "---") {
		// No frontmatter, return empty
		return &Frontmatter{}, content, nil
	}

	// Find the closing ---
	lines := strings.Split(str, "\n")
	if len(lines) < 2 {
		return nil, nil, fmt.Errorf("invalid frontmatter: too few lines")
	}

	// Find end of frontmatter
	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			endIdx = i
			break
		}
	}

	if endIdx == -1 {
		return nil, nil, fmt.Errorf("invalid frontmatter: closing --- not found")
	}

	// Extract frontmatter lines
	frontmatterLines := lines[1:endIdx]
	bodyLines := lines[endIdx+1:]

	// Parse frontmatter
	fm := &Frontmatter{
		Metadata: make(map[string]interface{}),
	}

	for _, line := range frontmatterLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse key: value
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Remove quotes if present
		if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
			value = value[1 : len(value)-1]
		} else if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
			value = value[1 : len(value)-1]
		}

		// Parse into struct fields
		switch key {
		case "name":
			fm.Name = value
		case "description":
			fm.Description = value
		case "license":
			fm.License = value
		case "compatibility":
			fm.Compatibility = value
		case "allowed-tools":
			// Parse as array
			fm.AllowedTools = parseArray(value)
		case "disable-model-invocation":
			// Parse as boolean
			fm.DisableModelInvocation = parseBool(value)
		default:
			// Store in metadata
			fm.Metadata[key] = value
		}
	}

	// Join body content
	body := strings.Join(bodyLines, "\n")

	return fm, []byte(body), nil
}

// parseArray parses a simple YAML array string.
func parseArray(s string) []string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil
	}

	s = s[1 : len(s)-1]
	parts := strings.Split(s, ",")
	result := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Remove quotes
		if strings.HasPrefix(part, "\"") && strings.HasSuffix(part, "\"") {
			part = part[1 : len(part)-1]
		} else if strings.HasPrefix(part, "'") && strings.HasSuffix(part, "'") {
			part = part[1 : len(part)-1]
		}
		result = append(result, part)
	}
	return result
}

// parseBool parses a boolean string.
func parseBool(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "true" || s == "yes" || s == "1"
}

// validateName validates a skill name per Agent Skills spec.
// Returns array of validation error messages (empty if valid).
func validateName(name, parentDirName string) []string {
	errors := []string{}

	// Name must match parent directory name
	if name != parentDirName {
		errors = append(errors, fmt.Sprintf(
			"name %q does not match parent directory %q",
			name, parentDirName,
		))
	}

	// Max length
	if len(name) > MaxNameLength {
		errors = append(errors, fmt.Sprintf(
			"name exceeds %d characters (%d)",
			MaxNameLength, len(name),
		))
	}

	// Must be lowercase a-z, 0-9, hyphens only
	matched, _ := regexp.MatchString("^[a-z0-9-]+$", name)
	if !matched {
		errors = append(errors,
			"name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)")
	}

	// Must not start or end with hyphen
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		errors = append(errors, "name must not start or end with a hyphen")
	}

	// Must not contain consecutive hyphens
	if strings.Contains(name, "--") {
		errors = append(errors, "name must not contain consecutive hyphens")
	}

	return errors
}

// validateDescription validates a skill description per Agent Skills spec.
func validateDescription(description string) []string {
	errors := []string{}

	trimmed := strings.TrimSpace(description)
	if trimmed == "" {
		errors = append(errors, "description is required")
	} else if len(trimmed) > MaxDescriptionLength {
		errors = append(errors, fmt.Sprintf(
			"description exceeds %d characters (%d)",
			MaxDescriptionLength, len(trimmed),
		))
	}

	return errors
}
