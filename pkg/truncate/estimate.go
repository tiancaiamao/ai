// Package truncate provides text truncation utilities with UTF-8 safety
package truncate

const (
	// ApproxBytesPerToken is the approximate number of bytes per token.
	// We use 4 as a conservative estimate (actual average is 3-4).
	ApproxBytesPerToken = 4
)

// CharsToTokens converts character count to token count.
func CharsToTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + ApproxBytesPerToken - 1) / ApproxBytesPerToken
}

// TokensToChars converts token count to character count.
func TokensToChars(tokens int) int {
	if tokens <= 0 {
		return 0
	}
	return tokens * ApproxBytesPerToken
}

// ApproxTokenCount estimates the token count of a text.
func ApproxTokenCount(text string) int {
	if text == "" {
		return 0
	}
	return (len(text) + ApproxBytesPerToken - 1) / ApproxBytesPerToken
}