package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/genius/ag/internal/conv"
)

// FormattedStreamWriter is a smart formatted stream writer.
type FormattedStreamWriter struct {
	file    *os.File
	writer  *bufio.Writer
	textBuf strings.Builder
}

// NewFormattedStreamWriter creates a new formatted stream writer.
func NewFormattedStreamWriter(filePath string) (*FormattedStreamWriter, error) {
	file, err := os.Create(filePath)
	if err != nil {
		return nil, err
	}

	return &FormattedStreamWriter{
		file:   file,
		writer: bufio.NewWriter(file),
	}, nil
}

// WriteJSONEvents writes a JSON event stream (from ai serve output).
func (w *FormattedStreamWriter) WriteJSONEvents(output []byte) error {
	scanner := bufio.NewScanner(strings.NewReader(string(output)))

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Try to parse as a JSON event
		formatted := conv.ParseEvent(line)
		if formatted != nil {
			// Write formatted text
			w.writeFormatted(formatted)
		} else {
			// Not a JSON event, treat as plain text
			w.writeRawText(line)
		}
	}

	return w.writer.Flush()
}

// WriteTextStream writes a text stream (for non-JSON output).
func (w *FormattedStreamWriter) WriteTextStream(output []byte) error {
	scanner := bufio.NewScanner(strings.NewReader(string(output)))

	for scanner.Scan() {
		line := scanner.Text()
		w.writeRawText(line)
	}

	return w.writer.Flush()
}

// writeFormatted writes a formatted event.
func (w *FormattedStreamWriter) writeFormatted(event *conv.FormattedEvent) {
	switch event.Kind {
	case conv.KindMeta:
		// Meta events get a timestamp and newline
		timestamp := time.Now().Format("15:04:05")
		fmt.Fprintf(w.writer, "[%s] %s\n", timestamp, event.Text)
	case conv.KindTool:
		// Tool events written directly
		fmt.Fprintf(w.writer, "%s\n", event.Text)
	case conv.KindText:
		// Text events get smart paragraphing
		w.writeSmartText(event.Text)
	}
}

// writeRawText writes raw text with smart paragraphing.
func (w *FormattedStreamWriter) writeRawText(text string) {
	w.writeSmartText(text)
}

// writeSmartText writes text with semantic paragraph segmentation.
func (w *FormattedStreamWriter) writeSmartText(text string) {
	// Accumulate into buffer
	w.textBuf.WriteString(text)

	// Check for complete semantic units
	bufferStr := w.textBuf.String()

	// Check for sentence endings
	if strings.ContainsAny(text, "。！？.!?") {
		// Write and clear buffer
		fmt.Fprintf(w.writer, "%s", bufferStr)
		w.textBuf.Reset()
		return
	}

	// Check for special lines (headings, lists, code blocks, etc.)
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "-") ||
		strings.HasPrefix(trimmed, "*") ||
		strings.HasPrefix(trimmed, "```") ||
		strings.HasPrefix(trimmed, "|") {
		// Write with preceding newline
		if w.textBuf.Len() > len(text) {
			// Buffer had prior content, write it first
			prevText := bufferStr[:len(bufferStr)-len(text)]
			fmt.Fprintf(w.writer, "%s", prevText)
		}
		fmt.Fprintf(w.writer, "\n%s", text)
		w.textBuf.Reset()
		return
	}

	// Force flush if buffer is too large
	if w.textBuf.Len() > 200 {
		fmt.Fprintf(w.writer, "%s", bufferStr)
		w.textBuf.Reset()
	}
}

// Flush writes any pending buffered content.
func (w *FormattedStreamWriter) Flush() error {
	// Write remaining buffer content
	if w.textBuf.Len() > 0 {
		fmt.Fprintf(w.writer, "%s", w.textBuf.String())
		w.textBuf.Reset()
	}

	return w.writer.Flush()
}

// Close flushes and closes the writer.
func (w *FormattedStreamWriter) Close() error {
	if err := w.Flush(); err != nil {
		return err
	}
	return w.file.Close()
}

// WriteFormattedOutput is a unified formatted writing interface.
func WriteFormattedOutput(agentDir string, output []byte, backendName string) error {
	streamPath := agentDir + "/stream.log"

	writer, err := NewFormattedStreamWriter(streamPath)
	if err != nil {
		return err
	}
	defer writer.Close()

	// Choose processing based on backend type
	if backendName == "ai" {
		// ai backend uses JSON event stream
		return writer.WriteJSONEvents(output)
	}
	// Other backends use text stream
	return writer.WriteTextStream(output)
}