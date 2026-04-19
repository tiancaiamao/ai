package task

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestImportPlanForwardDependency(t *testing.T) {
	setupTest(t)

	type planTask struct {
		ID           string   `yaml:"id"`
		Title        string   `yaml:"title"`
		Dependencies []string `yaml:"dependencies"`
	}
	type planDoc struct {
		Tasks []planTask `yaml:"tasks"`
	}

	// YAML with T002 -> T001, but T001 defined AFTER T002 (forward ref)
	yamlContent := `
tasks:
  - id: t002
    title: "Task Two"
    dependencies:
      - t001
  - id: t001
    title: "Task One"
`
	var doc planDoc
	if err := yaml.Unmarshal([]byte(yamlContent), &doc); err != nil {
		t.Fatalf("parse yaml: %v", err)
	}

	// Phase 1: create all tasks
	for _, pt := range doc.Tasks {
		_, err := CreateWithID(pt.ID, pt.Title, "")
		if err != nil {
			t.Fatalf("create %s: %v", pt.ID, err)
		}
	}

	// Phase 2: add dependencies
	for _, pt := range doc.Tasks {
		for _, depID := range pt.Dependencies {
			depID = strings.TrimSpace(depID)
			if depID == "" {
				continue
			}
			if _, err := AddDependency(pt.ID, depID); err != nil {
				t.Fatalf("AddDependency(%s, %s): %v", pt.ID, depID, err)
			}
		}
	}

	// Verify t002 depends on t001
	t2, err := Show("t002")
	if err != nil {
		t.Fatalf("show t002: %v", err)
	}
	if len(t2.Dependencies) != 1 || t2.Dependencies[0] != "t001" {
		t.Fatalf("t002 dependencies = %v, want [t001]", t2.Dependencies)
	}
	t.Logf("t002 dependencies = %v", t2.Dependencies)
}
