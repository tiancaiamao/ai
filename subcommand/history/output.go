package history

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/tiancaiamao/ai/pkg/session"
)

const (
	// maxOutputChars caps the payload of a single invocation (≈10k tokens).
	maxOutputChars = 40000
	// outputTruncatedMarker is appended when the cap is hit.
	outputTruncatedMarker = "…[output truncated at 40000 chars, refine your query]"
)

// boundedWriter accumulates output and enforces the per-invocation cap.
// In JSON mode each record is one JSONL line; when the cap is hit, no further
// records are emitted and a final machine-readable notice line is appended.
// In text mode the marker is appended inline.
type boundedWriter struct {
	json      bool
	buf       bytes.Buffer
	truncated bool
}

// emitRecord writes one record: text as-is, JSON marshalled to one line.
func (w *boundedWriter) emitRecord(text string, jsonValue any) {
	if w.truncated {
		return
	}
	chunk := text
	if w.json {
		data, err := json.Marshal(jsonValue)
		if err != nil {
			// Marshalling plain data structs cannot fail in practice; skip
			// the record rather than corrupting the JSONL stream.
			return
		}
		chunk = string(data) + "\n"
	}
	if utf8.RuneCountInString(w.buf.String())+utf8.RuneCountInString(chunk) > maxOutputChars {
		w.truncated = true
		return
	}
	w.buf.WriteString(chunk)
}

// flush writes the accumulated output plus the truncation marker if the cap
// was hit. The JSON notice is a separate JSONL line so per-line consumers
// (jq) can filter it out.
func (w *boundedWriter) flush(out io.Writer) {
	_, _ = out.Write(w.buf.Bytes())
	if !w.truncated {
		return
	}
	if w.json {
		fmt.Fprintf(out, "%s\n", `{"history_truncated":true,"notice":"`+outputTruncatedMarker+`"}`)
		return
	}
	fmt.Fprintln(out, outputTruncatedMarker)
}

// formatWindow renders one window as a human-readable text block.
func formatWindow(win session.HistoryWindow) string {
	summary := win.SummaryPreview
	if summary == "" {
		summary = "(no summary)"
	}
	return fmt.Sprintf("%s\tcreated=%s\ttokens_before=%d\titems=%d\n    summary: %s\n",
		win.WindowID, win.CreatedAt, win.TokensBefore, win.ItemCount, summary)
}

// formatItem renders one history item as a human-readable text block.
func formatItem(item session.HistoryItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\t%s\t%s\tchars=%d\n", item.EntryID, item.Role, item.Timestamp, item.TotalChars)
	if item.ToolName != "" {
		fmt.Fprintf(&b, "    tool: %s\n", item.ToolName)
	}
	content := strings.TrimRight(item.Content, "\n")
	if content == "" {
		content = "(empty)"
	}
	for _, line := range strings.Split(content, "\n") {
		fmt.Fprintf(&b, "    %s\n", line)
	}
	b.WriteString("\n")
	return b.String()
}

// formatSearchResult renders one search match as a human-readable text block.
func formatSearchResult(result session.HistorySearchResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\t%s\twindow=%s\t%s\n", result.EntryID, result.Role, result.WindowID, result.Timestamp)
	fmt.Fprintf(&b, "    %s\n\n", result.Match)
	return b.String()
}

// formatSummary renders the trailing "total vs shown" line for text mode.
// When totalKnown is false the total is a lower bound (the query layer
// clamped the result set), so it is rendered with a "+" suffix (design §5
// scenario 7: "100+") instead of masquerading as an exact count.
func formatSummary(kind string, shown, total int, totalKnown bool) string {
	if totalKnown && shown == total {
		return fmt.Sprintf("%d %s(s)\n", total, kind)
	}
	if totalKnown {
		return fmt.Sprintf("showing %d of %d %s(s)\n", shown, total, kind)
	}
	return fmt.Sprintf("showing %d of %d+ %s(s)\n", shown, total, kind)
}
