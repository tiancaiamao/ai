package llm

import "testing"

func imagePart(url string) ContentPart {
	return ContentPart{Type: "image_url", ImageURL: &struct {
		URL string `json:"url"`
	}{URL: url}}
}

func TestFilterUnsupportedContent(t *testing.T) {
	tests := []struct {
		name           string
		messages       []LLMMessage
		supportsVision bool
		wantImages     int // expected number of image parts in result
		wantRemoved    int
		wantOther      int // expected number of non-image parts in result
	}{
		{
			name: "vision model keeps images",
			messages: []LLMMessage{{
				Role: "user",
				ContentParts: []ContentPart{
					{Type: "text", Text: "Hello"},
					imagePart("data:image/png;base64,abc"),
				},
			}},
			supportsVision: true,
			wantImages:     1,
			wantRemoved:    0,
			wantOther:      1,
		},
		{
			name: "text-only model strips images",
			messages: []LLMMessage{{
				Role: "user",
				ContentParts: []ContentPart{
					{Type: "text", Text: "Hello"},
					imagePart("data:image/png;base64,abc"),
				},
			}},
			wantImages:  0,
			wantRemoved: 1,
			wantOther:   1,
		},
		{
			name: "text-only model strips all images across messages",
			messages: []LLMMessage{
				{Role: "user", Content: "Text only"},
				{Role: "assistant", ContentParts: []ContentPart{imagePart("a"), imagePart("b")}},
			},
			wantImages:  0,
			wantRemoved: 2,
			wantOther:   0,
		},
		{
			name: "unknown part types preserved",
			messages: []LLMMessage{{
				Role: "user",
				ContentParts: []ContentPart{
					{Type: "text", Text: "Hello"},
					{Type: "audio", Text: "podcast"},
				},
			}},
			wantImages:  0,
			wantRemoved: 0,
			wantOther:   2,
		},
		{
			name: "empty content parts preserved",
			messages: []LLMMessage{
				{Role: "user", ContentParts: []ContentPart{}},
			},
			wantImages:  0,
			wantRemoved: 0,
			wantOther:   0,
		},
		{
			name: "image-only message dropped when fully stripped",
			messages: []LLMMessage{
				{Role: "user", ContentParts: []ContentPart{imagePart("a")}},
			},
			wantImages:  0,
			wantRemoved: 1,
			wantOther:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, removed := FilterUnsupportedContent(tt.messages, tt.supportsVision)

			if removed != tt.wantRemoved {
				t.Errorf("removed = %d, want %d", removed, tt.wantRemoved)
			}

			images, other := 0, 0
			for _, msg := range got {
				for _, part := range msg.ContentParts {
					switch part.Type {
					case "image_url":
						images++
					default:
						other++
					}
				}
			}
			if images != tt.wantImages {
				t.Errorf("images = %d, want %d", images, tt.wantImages)
			}
			if other != tt.wantOther {
				t.Errorf("other parts = %d, want %d", other, tt.wantOther)
			}
		})
	}
}
