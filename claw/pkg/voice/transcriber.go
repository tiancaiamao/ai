// Package voice provides audio transcription capabilities for claw.
// Supports multiple providers: Groq (Whisper) and Zhipu (GLM-ASR).
package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Transcriber defines the interface for audio transcription.
type Transcriber interface {
	Transcribe(ctx context.Context, audioFilePath string) (*TranscriptionResult, error)
	IsAvailable() bool
}

// TranscriptionResult contains the result of audio transcription.
type TranscriptionResult struct {
	Text     string  `json:"text"`
	Language string  `json:"language,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

// ---- Groq Transcriber (Whisper API) ----

// GroqTranscriber implements Transcriber using Groq's Whisper API.
type GroqTranscriber struct {
	apiKey     string
	apiBase    string
	httpClient *http.Client
	model      string
}

// GroqConfig contains configuration for Groq transcriber.
type GroqConfig struct {
	APIKey  string
	APIBase string // optional, defaults to https://api.groq.com/openai/v1
	Model   string // optional, defaults to whisper-large-v3
	Timeout time.Duration
}

// NewGroqTranscriber creates a new Groq-based transcriber.
func NewGroqTranscriber(cfg GroqConfig) *GroqTranscriber {
	apiBase := cfg.APIBase
	if apiBase == "" {
		apiBase = "https://api.groq.com/openai/v1"
	}

	model := cfg.Model
	if model == "" {
		model = "whisper-large-v3"
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	return &GroqTranscriber{
		apiKey:  cfg.APIKey,
		apiBase: apiBase,
		model:   model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Transcribe transcribes an audio file to text.
func (t *GroqTranscriber) Transcribe(ctx context.Context, audioFilePath string) (*TranscriptionResult, error) {
	if !t.IsAvailable() {
		return nil, fmt.Errorf("transcriber not available: API key not configured")
	}

	audioFile, err := os.Open(audioFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer audioFile.Close()

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	part, err := writer.CreateFormFile("file", filepath.Base(audioFilePath))
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(part, audioFile); err != nil {
		return nil, fmt.Errorf("failed to copy file content: %w", err)
	}

	if err := writer.WriteField("model", t.model); err != nil {
		return nil, fmt.Errorf("failed to write model field: %w", err)
	}

	if err := writer.WriteField("response_format", "json"); err != nil {
		return nil, fmt.Errorf("failed to write response_format field: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	url := t.apiBase + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, &requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var result TranscriptionResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// IsAvailable returns true if the transcriber is properly configured.
func (t *GroqTranscriber) IsAvailable() bool {
	return t.apiKey != ""
}

// ---- Zhipu Transcriber (GLM-ASR API) ----

// ZhipuTranscriber implements Transcriber using Zhipu's GLM-ASR API.
type ZhipuTranscriber struct {
	apiKey     string
	apiBase    string
	httpClient *http.Client
}

// ZhipuConfig contains configuration for Zhipu transcriber.
type ZhipuConfig struct {
	APIKey  string
	APIBase string // optional, defaults to https://open.bigmodel.cn/api/paas/v4
	Timeout time.Duration
}

// NewZhipuTranscriber creates a new Zhipu-based transcriber.
func NewZhipuTranscriber(cfg ZhipuConfig) *ZhipuTranscriber {
	apiBase := cfg.APIBase
	if apiBase == "" {
		apiBase = "https://open.bigmodel.cn/api/paas/v4"
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	return &ZhipuTranscriber{
		apiKey:  cfg.APIKey,
		apiBase: apiBase,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Transcribe transcribes an audio file to text using Zhipu GLM-ASR.
func (t *ZhipuTranscriber) Transcribe(ctx context.Context, audioFilePath string) (*TranscriptionResult, error) {
	if !t.IsAvailable() {
		return nil, fmt.Errorf("transcriber not available: API key not configured")
	}

	audioFile, err := os.Open(audioFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer audioFile.Close()

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	part, err := writer.CreateFormFile("file", filepath.Base(audioFilePath))
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(part, audioFile); err != nil {
		return nil, fmt.Errorf("failed to copy file content: %w", err)
	}

	// Zhipu uses "glm-asr-2512" model (see: https://docs.bigmodel.cn/cn/guide/models/sound-and-video/glm-asr-2512)
	if err := writer.WriteField("model", "glm-asr-2512"); err != nil {
		return nil, fmt.Errorf("failed to write model field: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	url := t.apiBase + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, &requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var result TranscriptionResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// IsAvailable returns true if the transcriber is properly configured.
func (t *ZhipuTranscriber) IsAvailable() bool {
	return t.apiKey != ""
}

// ---- Helper functions ----

// IsAudioFile checks if a file is an audio file based on its extension.
func IsAudioFile(filename string) bool {
	audioExtensions := []string{".mp3", ".wav", ".ogg", ".m4a", ".flac", ".aac", ".wma", ".webm"}
	lowerFilename := strings.ToLower(filename)
	for _, ext := range audioExtensions {
		if strings.HasSuffix(lowerFilename, ext) {
			return true
		}
	}
	return false
}