package winai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/rpc"
)

func TestResolveModelInputPrefersExactMatch(t *testing.T) {
	p := newBaseInterpreter()
	p.availableModels = []rpc.ModelInfo{
		{Provider: "zai", ID: "glm-4.7-flash", Name: "GLM 4.7 Flash"},
		{Provider: "zai", ID: "glm-4.7", Name: "GLM 4.7"},
	}

	model, err := p.resolveModelInput("glm-4.7")
	if err != nil {
		t.Fatalf("resolveModelInput returned error: %v", err)
	}
	if model.ID != "glm-4.7" {
		t.Fatalf("model ID = %q, want %q", model.ID, "glm-4.7")
	}
}

func TestResolveModelInputAmbiguousPrefix(t *testing.T) {
	p := newBaseInterpreter()
	p.availableModels = []rpc.ModelInfo{
		{Provider: "zai", ID: "glm-4.7-flash", Name: "GLM 4.7 Flash"},
		{Provider: "zai", ID: "glm-4.7", Name: "GLM 4.7"},
	}

	_, err := p.resolveModelInput("glm-4")
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %q, want to contain %q", err.Error(), "ambiguous")
	}
}

func TestHandleAvailableModelsSortsDeterministic(t *testing.T) {
	p := newBaseInterpreter()
	payload := struct {
		Models []rpc.ModelInfo `json:"models"`
	}{
		Models: []rpc.ModelInfo{
			{Provider: "zai", ID: "glm-5"},
			{Provider: "anthropic", ID: "claude-sonnet-4-20250514"},
			{Provider: "zai", ID: "glm-4.7"},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	p.handleAvailableModels(data)
	if len(p.availableModels) != 3 {
		t.Fatalf("available models len = %d, want 3", len(p.availableModels))
	}

	got := []string{
		p.availableModels[0].Provider + "/" + p.availableModels[0].ID,
		p.availableModels[1].Provider + "/" + p.availableModels[1].ID,
		p.availableModels[2].Provider + "/" + p.availableModels[2].ID,
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
