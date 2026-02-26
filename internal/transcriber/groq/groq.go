package groq

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
)

const (
	transcriptionURL = "https://api.groq.com/openai/v1/audio/transcriptions"
	maxBytes         = 25 * 1024 * 1024 // 25MB Groq limit
)

type Transcriber struct {
	apiKey string
	client *http.Client
}

func New(apiKey string) *Transcriber {
	return &Transcriber{
		apiKey: apiKey,
		client: &http.Client{},
	}
}

// Transcribe sends audio to Groq Whisper API.
// Files over 25MB are not supported and will return an error.
func (t *Transcriber) Transcribe(ctx context.Context, audioPath string) (string, error) {
	info, err := os.Stat(audioPath)
	if err != nil {
		return "", fmt.Errorf("stat audio file: %w", err)
	}

	if info.Size() > maxBytes {
		return "", fmt.Errorf("audio file exceeds 25MB limit (%d bytes); please use a shorter recording", info.Size())
	}

	return t.transcribeFile(ctx, audioPath)
}

func (t *Transcriber) transcribeFile(ctx context.Context, audioPath string) (string, error) {
	f, err := os.Open(audioPath)
	if err != nil {
		return "", fmt.Errorf("open audio: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	if err := w.WriteField("model", "whisper-large-v3"); err != nil {
		return "", err
	}
	if err := w.WriteField("language", "pt"); err != nil {
		return "", err
	}
	if err := w.WriteField("response_format", "text"); err != nil {
		return "", err
	}

	part, err := w.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, transcriptionURL, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("groq request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("groq API error %d: %s", resp.StatusCode, groqErrorMessage(body))
	}

	return strings.TrimSpace(string(body)), nil
}

// groqErrorMessage extracts a human-readable error from a Groq API error response.
func groqErrorMessage(body []byte) string {
	var v struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &v); err == nil && v.Error.Message != "" {
		return v.Error.Message
	}
	return string(body)
}
