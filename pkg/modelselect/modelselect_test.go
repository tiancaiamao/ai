package modelselect

import (
	"testing"
)

type testModel struct {
	Provider string
	ID       string
	Name     string
}

func keyOfModel(m testModel) Keys {
	return Keys{Provider: m.Provider, ID: m.ID, Name: m.Name}
}

func TestSortByModelKeyDeterministic(t *testing.T) {
	models := []testModel{
		{Provider: "zai", ID: "glm-5"},
		{Provider: "anthropic", ID: "claude-sonnet-4-20250514"},
		{Provider: "zai", ID: "glm-4.7"},
	}

	SortByModelKey(models, keyOfModel)

	got := []string{
		models[0].Provider + "/" + models[0].ID,
		models[1].Provider + "/" + models[1].ID,
		models[2].Provider + "/" + models[2].ID,
	}
	want := []string{
		"anthropic/claude-sonnet-4-20250514",
		"zai/glm-4.7",
		"zai/glm-5",
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
