package llm

// FilterUnsupportedContent removes content parts the model doesn't support.
// Image parts are removed when the model doesn't support vision, and video
// parts are removed when the model doesn't support video — the case where a
// session created with a multimodal model is resumed with a text-only model
// would otherwise cause API errors.
//
// It returns the filtered messages and the number of content parts removed.
func FilterUnsupportedContent(messages []LLMMessage, supportsVision, supportsVideo bool) ([]LLMMessage, int) {
	if supportsVision && supportsVideo {
		return messages, 0
	}

	removed := 0
	filtered := make([]LLMMessage, 0, len(messages))
	for _, msg := range messages {
		if len(msg.ContentParts) == 0 {
			filtered = append(filtered, msg)
			continue
		}

		parts := make([]ContentPart, 0, len(msg.ContentParts))
		for _, part := range msg.ContentParts {
			switch part.Type {
			case "image_url":
				if !supportsVision {
					removed++
					continue
				}
			case "video_url":
				if !supportsVideo {
					removed++
					continue
				}
			}
			parts = append(parts, part)
		}

		msg.ContentParts = parts
		// Drop the message only if nothing at all remains.
		if len(parts) > 0 || msg.Content != "" {
			filtered = append(filtered, msg)
		}
	}
	return filtered, removed
}
