package llm

import (
	"fmt"
)

// FilterMessagesForCapability filters out content parts that are not supported
// by the model's capabilities. For example, if the model doesn't support vision,
// image_url content parts are removed from messages.
//
// This is used when switching between models with different capabilities
// to avoid API errors from unsupported content types.
func FilterMessagesForCapability(messages []LLMMessage, capabilities Capability) []LLMMessage {
	filtered := make([]LLMMessage, 0, len(messages))

	for _, msg := range messages {
		// If there are no content parts, keep as-is
		if len(msg.ContentParts) == 0 {
			filtered = append(filtered, msg)
			continue
		}

		// Filter content parts based on model capabilities
		filteredParts := make([]ContentPart, 0, len(msg.ContentParts))
		for _, part := range msg.ContentParts {
			switch part.Type {
			case "text":
				// All models support text
				filteredParts = append(filteredParts, part)

			case "image_url":
				// Only add if model supports vision
				if capabilities.SupportsVision() {
					filteredParts = append(filteredParts, part)
				}
				// Otherwise, silently drop the image

			default:
				// Keep unknown parts (don't break on new content types)
				filteredParts = append(filteredParts, part)
			}
		}

		// If we still have content parts, keep the message
		if len(filteredParts) > 0 {
			msg.ContentParts = filteredParts
			filtered = append(filtered, msg)
		} else if msg.Content != "" {
			// Content string is present without parts, keep as-is
			filtered = append(filtered, msg)
		}
		// If no content string and no parts, drop the message
	}

	return filtered
}

// DetectUnsupportedContent returns a description of unsupported content
// in the messages for the given capabilities. Returns empty string if
// all content is supported.
func DetectUnsupportedContent(messages []LLMMessage, capabilities Capability) string {
	if capabilities.SupportsVision() {
		return "" // All content types supported
	}

	imageCount := 0
	for _, msg := range messages {
		for _, part := range msg.ContentParts {
			if part.Type == "image_url" {
				imageCount++
			}
		}
	}

	if imageCount > 0 {
		return fmt.Sprintf("%d image(s) found in conversation history", imageCount)
	}

	return ""
}
