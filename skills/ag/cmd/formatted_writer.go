package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/genius/ag/internal/conv"
)

// FormattedStreamWriter 智能格式化的流式写入器
type FormattedStreamWriter struct {
	file    *os.File
	writer  *bufio.Writer
	textBuf strings.Builder
}

// NewFormattedStreamWriter 创建新的格式化写入器
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

// WriteJSONEvents 写入 JSON 事件流（用于 ai serve 输出）
func (w *FormattedStreamWriter) WriteJSONEvents(output []byte) error {
	scanner := bufio.NewScanner(strings.NewReader(string(output)))

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// 尝试解析为 JSON 事件
		formatted := conv.ParseEvent(line)
		if formatted != nil {
			// 写入格式化的文本
			w.writeFormatted(formatted)
		} else {
			// 如果不是 JSON 事件，作为纯文本处理
			w.writeRawText(line)
		}
	}

	return w.writer.Flush()
}

// WriteTextStream 写入文本流（用于非 JSON 格式的输出）
func (w *FormattedStreamWriter) WriteTextStream(output []byte) error {
	scanner := bufio.NewScanner(strings.NewReader(string(output)))

	for scanner.Scan() {
		line := scanner.Text()
		w.writeRawText(line)
	}

	return w.writer.Flush()
}

// writeFormatted 写入格式化的事件
func (w *FormattedStreamWriter) writeFormatted(event *conv.FormattedEvent) {
	switch event.Kind {
	case conv.KindMeta:
		// 元数据事件，添加时间戳和换行
		timestamp := time.Now().Format("15:04:05")
		fmt.Fprintf(w.writer, "[%s] %s\n", timestamp, event.Text)
	case conv.KindTool:
		// 工具事件，直接写入
		fmt.Fprintf(w.writer, "%s\n", event.Text)
	case conv.KindText:
		// 文本事件，智能分段
		w.writeSmartText(event.Text)
	}
}

// writeRawText 写入原始文本，进行智能分段
func (w *FormattedStreamWriter) writeRawText(text string) {
	w.writeSmartText(text)
}

// writeSmartText 智能写入文本，按语义分段
func (w *FormattedStreamWriter) writeSmartText(text string) {
	// 累积到缓冲区
	w.textBuf.WriteString(text)

	// 检查是否有完整的语义单元
	bufferStr := w.textBuf.String()

	// 检查句子结束
	if strings.ContainsAny(text, "。！？.!?") {
		// 写入并清空缓冲区
		fmt.Fprintf(w.writer, "%s", bufferStr)
		w.textBuf.Reset()
		return
	}

	// 检查是否是特殊行（标题、列表、代码块等）
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "-") ||
		strings.HasPrefix(trimmed, "*") ||
		strings.HasPrefix(trimmed, "```") ||
		strings.HasPrefix(trimmed, "|") {
		// 换行后写入
		if w.textBuf.Len() > len(text) {
			// 说明缓冲区之前有内容，先写入之前的
			prevText := bufferStr[:len(bufferStr)-len(text)]
			fmt.Fprintf(w.writer, "%s", prevText)
		}
		fmt.Fprintf(w.writer, "\n%s", text)
		w.textBuf.Reset()
		return
	}

	// 如果缓冲区太大，强制写入
	if w.textBuf.Len() > 200 {
		fmt.Fprintf(w.writer, "%s", bufferStr)
		w.textBuf.Reset()
	}
}

// Flush 刷新所有待写入的内容
func (w *FormattedStreamWriter) Flush() error {
	// 写入剩余的缓冲区内容
	if w.textBuf.Len() > 0 {
		fmt.Fprintf(w.writer, "%s", w.textBuf.String())
		w.textBuf.Reset()
	}

	return w.writer.Flush()
}

// Close 关闭写入器
func (w *FormattedStreamWriter) Close() error {
	if err := w.Flush(); err != nil {
		return err
	}
	return w.file.Close()
}

// WriteFormattedOutput 统一的格式化写入接口
func WriteFormattedOutput(agentDir string, output []byte, backendName string) error {
	streamPath := agentDir + "/stream.log"

	writer, err := NewFormattedStreamWriter(streamPath)
	if err != nil {
		return err
	}
	defer writer.Close()

	// 根据 backend 类型选择不同的处理方式
	if backendName == "ai" {
		// ai backend 使用 JSON 事件流
		return writer.WriteJSONEvents(output)
	}
	// 其他 backend 使用文本流
	return writer.WriteTextStream(output)
}
