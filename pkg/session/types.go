package session

// SessionFileSummary represents session file metadata.
type SessionFileSummary struct {
	File         string `json:"file"`
	SessionID    string `json:"sessionId"`
	MessageCount int    `json:"messageCount"`
}

// TreeEntry represents a tree-structured session entry for internal use.
// Note: This is the internal session type. The RPC type is in pkg/app.
type TreeEntry struct {
	EntryID   string  `json:"entryId"`
	ParentID  *string `json:"parentId,omitempty"`
	Type      string  `json:"type"`
	Role      string  `json:"role"`
	Text      string  `json:"text"`
	Timestamp string  `json:"timestamp"`
	Depth     int     `json:"depth"`
	Leaf      bool    `json:"leaf"`
}

// SessionTokenStats represents token usage statistics for internal use.
// Note: This is the internal session type. The RPC type is in pkg/app.
type SessionTokenStats struct {
	Input        int `json:"input"`
	Output       int `json:"output"`
	Total        int `json:"total"`
	SystemPrompt int `json:"systemPrompt"`
	SystemTools  int `json:"systemTools"`
	TotalContext int `json:"totalContext"`
	ActiveWindow int `json:"activeWindow"`
}
