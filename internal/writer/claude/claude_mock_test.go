package claude

// Mock Anthropic Messages API server with full request validation.
//
// The Anthropic API expects:
//   POST /v1/messages
//   Content-Type: application/json
//   x-api-key: <key>
//   Body: {"model":"...","max_tokens":4096,"messages":[{"role":"user","content":[...]}]}
//
// The tests below exercise GenerateArticle via newWriterForTest, which points
// the Anthropic SDK client at the mock server URL.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// capturedAnthropicRequest holds the decoded Anthropic request body fields.
type capturedAnthropicRequest struct {
	path      string
	apiKeyHdr string
	model     string
	maxTokens int
	messages  []anthropicMessage
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// anthropicMockServer is an httptest.Server that validates Anthropic API requests.
type anthropicMockServer struct {
	srv      *httptest.Server
	Captured capturedAnthropicRequest
	code     int
	respBody []byte
}

func newAnthropicMock(t *testing.T, code int, articleText string) *anthropicMockServer {
	t.Helper()
	m := &anthropicMockServer{
		code:     code,
		respBody: anthropicMessagesResponse(articleText),
	}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Captured.path = r.URL.Path
		m.Captured.apiKeyHdr = r.Header.Get("x-api-key")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var req struct {
			Model     string             `json:"model"`
			MaxTokens int                `json:"max_tokens"`
			Messages  []anthropicMessage `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("parsing request body: %v", err)
		}
		m.Captured.model = req.Model
		m.Captured.maxTokens = req.MaxTokens
		m.Captured.messages = req.Messages

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(m.code)
		if m.code == http.StatusOK {
			w.Write(m.respBody)
		} else {
			w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"mock error"}}`))
		}
	}))
	return m
}

// firstMessageText returns the text of the first content block of the first message.
func (c *capturedAnthropicRequest) firstMessageText() string {
	if len(c.messages) == 0 || len(c.messages[0].Content) == 0 {
		return ""
	}
	return c.messages[0].Content[0].Text
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestClaude_UsesConfiguredModel verifies that the model sent to the API
// matches what was configured in the Writer.
func TestClaude_UsesConfiguredModel(t *testing.T) {
	m := newAnthropicMock(t, http.StatusOK, validArticleJSON())
	defer m.srv.Close()

	wr := newWriterForTest(t, m.srv)
	if _, err := wr.GenerateArticle(context.Background(), "transcrição", "Título"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := m.Captured.model, "claude-test-model"; got != want {
		t.Errorf("model = %q, want %q", got, want)
	}
}

// TestClaude_MaxTokensIs4096 verifies that max_tokens is always 4096.
func TestClaude_MaxTokensIs4096(t *testing.T) {
	m := newAnthropicMock(t, http.StatusOK, validArticleJSON())
	defer m.srv.Close()

	wr := newWriterForTest(t, m.srv)
	if _, err := wr.GenerateArticle(context.Background(), "transcrição", "Título"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := m.Captured.maxTokens, 4096; got != want {
		t.Errorf("max_tokens = %d, want %d", got, want)
	}
}

// TestClaude_MessageRoleIsUser verifies that the first message has role="user".
func TestClaude_MessageRoleIsUser(t *testing.T) {
	m := newAnthropicMock(t, http.StatusOK, validArticleJSON())
	defer m.srv.Close()

	wr := newWriterForTest(t, m.srv)
	if _, err := wr.GenerateArticle(context.Background(), "transcrição", "Título"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(m.Captured.messages) == 0 {
		t.Fatal("no messages captured")
	}
	if got, want := m.Captured.messages[0].Role, "user"; got != want {
		t.Errorf("messages[0].role = %q, want %q", got, want)
	}
}

// TestClaude_PromptContainsTranscript verifies that the user message text
// contains the full transcript.
func TestClaude_PromptContainsTranscript(t *testing.T) {
	m := newAnthropicMock(t, http.StatusOK, validArticleJSON())
	defer m.srv.Close()

	transcript := "os vikings venceram o jogo por 28 a 14"
	wr := newWriterForTest(t, m.srv)
	if _, err := wr.GenerateArticle(context.Background(), transcript, "Título"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := m.Captured.firstMessageText()
	if !strings.Contains(text, transcript) {
		t.Errorf("prompt does not contain transcript %q", transcript)
	}
}

// TestClaude_PromptContainsMediaTitle verifies that the media title is
// included in the prompt sent to Claude.
func TestClaude_PromptContainsMediaTitle(t *testing.T) {
	m := newAnthropicMock(t, http.StatusOK, validArticleJSON())
	defer m.srv.Close()

	mediaTitle := "Episódio 42: Análise do Draft"
	wr := newWriterForTest(t, m.srv)
	if _, err := wr.GenerateArticle(context.Background(), "transcrição", mediaTitle); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := m.Captured.firstMessageText()
	if !strings.Contains(text, mediaTitle) {
		t.Errorf("prompt does not contain media title %q", mediaTitle)
	}
}

// TestClaude_SendsAPIKey verifies that the x-api-key header is present.
func TestClaude_SendsAPIKey(t *testing.T) {
	m := newAnthropicMock(t, http.StatusOK, validArticleJSON())
	defer m.srv.Close()

	wr := newWriterForTest(t, m.srv)
	if _, err := wr.GenerateArticle(context.Background(), "transcrição", "Título"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.Captured.apiKeyHdr == "" {
		t.Error("x-api-key header was not sent")
	}
}

// TestClaude_EmptyResponseContentReturnsError verifies that when the API
// returns an empty content array, GenerateArticle returns an error.
func TestClaude_EmptyResponseContentReturnsError(t *testing.T) {
	// Return a valid 200 but with an empty content array.
	emptyResp := `{
		"id":"msg_test","type":"message","role":"assistant",
		"content":[],"model":"claude-test-model","stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":0}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(emptyResp))
	}))
	defer srv.Close()

	wr := newWriterForTest(t, srv)
	_, err := wr.GenerateArticle(context.Background(), "transcrição", "Título")
	if err == nil {
		t.Fatal("expected error for empty content, got nil")
	}
}

// TestClaude_401UnauthorizedReturnsError verifies that HTTP 401 from the API
// propagates as an error.
func TestClaude_401UnauthorizedReturnsError(t *testing.T) {
	m := newAnthropicMock(t, http.StatusUnauthorized, "")
	defer m.srv.Close()

	wr := newWriterForTest(t, m.srv)
	_, err := wr.GenerateArticle(context.Background(), "transcrição", "Título")
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}

// TestClaude_RequestPathIsMessages verifies that requests go to /v1/messages.
func TestClaude_RequestPathIsMessages(t *testing.T) {
	m := newAnthropicMock(t, http.StatusOK, validArticleJSON())
	defer m.srv.Close()

	wr := newWriterForTest(t, m.srv)
	if _, err := wr.GenerateArticle(context.Background(), "transcrição", "Título"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasSuffix(m.Captured.path, "/messages") {
		t.Errorf("path = %q, want suffix %q", m.Captured.path, "/messages")
	}
}

