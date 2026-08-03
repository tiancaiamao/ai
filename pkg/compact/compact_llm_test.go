package compact

import (
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func imageMessage() []agentctx.AgentMessage {
	return []agentctx.AgentMessage{
		agentctx.NewUserMessage("what is in this image?"),
		{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Text: "here:"},
				agentctx.ImageContent{Type: "image", Data: "base64data", MimeType: "image/png"},
			},
		},
	}
}

func TestBuildCacheFriendlyLLMContext_FiltersImagesForTextOnlyModel(t *testing.T) {
	for _, supportsVision := range []bool{false, true} {
		llmCtx := buildCacheFriendlyLLMContext(
			imageMessage(),
			"system",
			"",
			nil,
			"summarize",
			"",
			supportsVision,
		)

		images := 0
		for _, msg := range llmCtx.Messages {
			for _, part := range msg.ContentParts {
				if part.Type == "image_url" {
					images++
				}
			}
		}

		if supportsVision && images != 1 {
			t.Errorf("supportsVision=true: image_url parts = %d, want 1", images)
		}
		if !supportsVision && images != 0 {
			t.Errorf("supportsVision=false: image_url parts = %d, want 0", images)
		}
	}
}
