package llm

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/model"
)

func TestFilterMessagesForCapability(t *testing.T) {
	tests := []struct {
		name         string
		messages     []LLMMessage
		capabilities model.Capability
		wantText     bool
		wantImage    bool
	}{
		{
			name: "text only model removes images",
			messages: []LLMMessage{
				{
					Role: "user",
					ContentParts: []ContentPart{
						{Type: "text", Text: "Hello"},
						{Type: "image_url", ImageURL: &struct {
							URL string `json:"url"`
						}{URL: "data:image/png;base64,abc"}},
					},
				},
			},
			capabilities: CapabilityText,
			wantText:     true,
			wantImage:    false,
		},
		{
			name: "vision model keeps images",
			messages: []LLMMessage{
				{
					Role: "user",
					ContentParts: []ContentPart{
						{Type: "text", Text: "Hello"},
						{Type: "image_url", ImageURL: &struct {
							URL string `json:"url"`
						}{URL: "data:image/png;base64,abc"}},
					},
				},
			},
			capabilities: CapabilityText | CapabilityVision,
			wantText:     true,
			wantImage:    true,
		},
		{
			name: "multiple messages - partial filtering",
			messages: []LLMMessage{
				{
					Role:    "user",
					Content: "Text only",
				},
				{
					Role: "assistant",
					ContentParts: []ContentPart{
						{Type: "text", Text: "Response"},
						{Type: "image_url", ImageURL: &struct {
							URL string `json:"url"`
						}{URL: "data:image/png;base64,abc"}},
					},
				},
			},
			capabilities: CapabilityText,
			wantText:     true,
			wantImage:    false,
		},
		{
			name: "empty message preserved",
			messages: []LLMMessage{
				{
					Role:         "user",
					ContentParts: []ContentPart{},
				},
			},
			capabilities: CapabilityText,
			wantText:     false,
			wantImage:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterMessagesForCapability(tt.messages, tt.capabilities)

			if len(got) == 0 && (tt.wantText || tt.wantImage) {
				t.Errorf("FilterMessagesForCapability() returned no messages, expected some")
				return
			}

			for _, msg := range got {
				for _, part := range msg.ContentParts {
					if part.Type == "text" && !tt.wantText {
						t.Errorf("FilterMessagesForCapability() kept text when expected to remove it")
					}
					if part.Type == "image_url" && !tt.wantImage {
						t.Errorf("FilterMessagesForCapability() kept image when expected to remove it")
					}
				}
			}

			if tt.wantText {
				foundText := false
				for _, msg := range got {
					if msg.Content != "" {
						foundText = true
						break
					}
					for _, part := range msg.ContentParts {
						if part.Type == "text" {
							foundText = true
							break
						}
					}
				}
				if !foundText {
					t.Errorf("FilterMessagesForCapability() removed all text content")
				}
			}

			if tt.wantImage {
				foundImage := false
				for _, msg := range got {
					for _, part := range msg.ContentParts {
						if part.Type == "image_url" {
							foundImage = true
							break
						}
					}
				}
				if !foundImage {
					t.Errorf("FilterMessagesForCapability() removed all image content")
				}
			}
		})
	}
}

func TestDetectUnsupportedContent(t *testing.T) {
	tests := []struct {
		name         string
		messages     []LLMMessage
		capabilities model.Capability
		wantContains string
	}{
		{
			name: "vision model - no unsupported",
			messages: []LLMMessage{
				{
					Role: "user",
					ContentParts: []ContentPart{
						{Type: "text", Text: "Hello"},
						{Type: "image_url", ImageURL: &struct {
							URL string `json:"url"`
						}{URL: "data:image/png;base64,abc"}},
					},
				},
			},
			capabilities: CapabilityText | CapabilityVision,
			wantContains: "",
		},
		{
			name: "text only model - detects image",
			messages: []LLMMessage{
				{
					Role: "user",
					ContentParts: []ContentPart{
						{Type: "text", Text: "Hello"},
						{Type: "image_url", ImageURL: &struct {
							URL string `json:"url"`
						}{URL: "data:image/png;base64,abc"}},
					},
				},
			},
			capabilities: CapabilityText,
			wantContains: "1 image",
		},
		{
			name: "text only model - multiple images",
			messages: []LLMMessage{
				{
					Role: "user",
					ContentParts: []ContentPart{
						{Type: "image_url", ImageURL: &struct {
							URL string `json:"url"`
						}{URL: "data:image/png;base64,abc"}},
						{Type: "image_url", ImageURL: &struct {
							URL string `json:"url"`
						}{URL: "data:image/png;base64,def"}},
					},
				},
			},
			capabilities: CapabilityText,
			wantContains: "2 image",
		},
		{
			name: "text only model - no images",
			messages: []LLMMessage{
				{
					Role:    "user",
					Content: "Hello",
				},
			},
			capabilities: CapabilityText,
			wantContains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectUnsupportedContent(tt.messages, tt.capabilities)

			if tt.wantContains == "" {
				if got != "" {
					t.Errorf("DetectUnsupportedContent() = %q, want empty string", got)
				}
				return
			}

			if got == "" {
				t.Errorf("DetectUnsupportedContent() = empty, want string containing %q", tt.wantContains)
				return
			}

			contains := false
			for i := 0; i <= len(got)-len(tt.wantContains); i++ {
				if got[i:i+len(tt.wantContains)] == tt.wantContains {
					contains = true
					break
				}
			}
			if !contains {
				t.Errorf("DetectUnsupportedContent() = %q, does not contain %q", got, tt.wantContains)
			}
		})
	}
}
