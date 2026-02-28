package memory

// MemorySource identifies where a memory entry originates
type MemorySource string

const (
	// MemorySourceDetail is the LLM-written notes and summaries
	MemorySourceDetail MemorySource = "detail"
	// MemorySourceMessages is the raw conversation log
	MemorySourceMessages MemorySource = "messages"
)

// SearchResult represents a retrieved memory entry
type SearchResult struct {
	// Source indicates where this result came from
	Source MemorySource `json:"source"`
	// FilePath is the file path for detail source (relative to session dir)
	FilePath string `json:"file_path,omitempty"`
	// LineNumber is the line where the match was found
	LineNumber int `json:"line_number,omitempty"`
	// Text is the matched content snippet
	Text string `json:"text"`
	// Citation is a human-readable reference for the source
	Citation string `json:"citation"`
}

// SearchOptions configures memory retrieval
type SearchOptions struct {
	// Query is the search query string
	Query string `json:"query"`
	// Limit is the maximum number of results to return
	Limit int `json:"limit"`
	// Sources filters which memory sources to search (nil = all)
	Sources []MemorySource `json:"sources,omitempty"`
}

// DefaultSearchOptions returns sensible defaults
func DefaultSearchOptions() SearchOptions {
	return SearchOptions{
		Limit:   5,
		Sources: nil, // Search all sources
	}
}
