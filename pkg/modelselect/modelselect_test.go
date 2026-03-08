package modelselect

import (
	"errors"
	"testing"
)

type testModel struct {
	Provider string
	ID       string
	Name     string
}

func keyOfModel(m testModel) Keys {
	return Keys{
		Provider: m.Provider,
		ID:       m.ID,
		Name:     m.Name,
	}
}

func TestSelectByQueryPrefersExactMatch(t *testing.T) {
	models := []testModel{
		{Provider: "zai", ID: "glm-4.7-flash", Name: "GLM 4.7 Flash"},
		{Provider: "zai", ID: "glm-4.7", Name: "GLM 4.7"},
	}

	selected, err := SelectByQuery(models, "glm-4.7", keyOfModel)
	if err != nil {
		t.Fatalf("SelectByQuery error: %v", err)
	}
	if selected.ID != "glm-4.7" {
		t.Fatalf("selected id = %q, want %q", selected.ID, "glm-4.7")
	}
}

func TestSelectByQueryAmbiguousPrefix(t *testing.T) {
	models := []testModel{
		{Provider: "zai", ID: "glm-4.7-flash", Name: "GLM 4.7 Flash"},
		{Provider: "zai", ID: "glm-4.7", Name: "GLM 4.7"},
	}

	_, err := SelectByQuery(models, "glm-4", keyOfModel)
	if err == nil {
		t.Fatal("expected ambiguous error, got nil")
	}
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("error = %v, want ErrAmbiguous", err)
	}
}

func TestSelectByQueryNotFound(t *testing.T) {
	models := []testModel{
		{Provider: "zai", ID: "glm-4.7", Name: "GLM 4.7"},
	}

	_, err := SelectByQuery(models, "gpt-4o", keyOfModel)
	if err == nil {
		t.Fatal("expected not found error, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
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
