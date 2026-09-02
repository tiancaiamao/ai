package protocol

import (
	"encoding/json"
	"testing"
)

func TestParseSetConfigParamsMergesFlatAndNestedFields(t *testing.T) {
	tests := []struct {
		name   string
		params string
		wantID string
		wantV  string
	}{
		{
			name:   "flat id nested value",
			params: `{"configId":"model","configuration":{"value":"zai/glm"}}`,
			wantID: "model",
			wantV:  "zai/glm",
		},
		{
			name:   "nested id flat value",
			params: `{"value":"zai/glm","configuration":{"id":"model"}}`,
			wantID: "model",
			wantV:  "zai/glm",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotValue, ok := parseSetConfigParams(json.RawMessage(tt.params))
			if !ok || gotID != tt.wantID || gotValue != tt.wantV {
				t.Fatalf("parseSetConfigParams() = (%q, %q, %t), want (%q, %q, true)", gotID, gotValue, ok, tt.wantID, tt.wantV)
			}
		})
	}
}
