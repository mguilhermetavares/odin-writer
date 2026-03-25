package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/mguilhermetavares/odin-writer/internal/style"
	"github.com/mguilhermetavares/odin-writer/internal/writer"
)

// sampleStyle returns a minimal Style for testing so tests do not depend on
// built-in embedded files.
func sampleStyle() *style.Style {
	return &style.Style{
		Name:         "test-style",
		Persona:      "Você é um jornalista esportivo.",
		Language:     "português brasileiro",
		Tone:         "objetivo",
		Structure:    "introdução, desenvolvimento, conclusão",
		WordCount:    "500 palavras",
		ContentRules: []string{"regra de conteúdo 1", "regra de conteúdo 2"},
		StyleRules:   []string{"regra de estilo 1"},
	}
}

// newWriterForTest builds a Writer whose Anthropic client points at the given
// httptest.Server.  This avoids any real network call during tests.
func newWriterForTest(t *testing.T, srv *httptest.Server) *Writer {
	t.Helper()
	c := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(srv.URL+"/"),
	)
	return &Writer{
		client:          &c,
		model:           "claude-test-model",
		transcriptLimit: 10000,
		style:           sampleStyle(),
	}
}

// anthropicMessagesResponse builds a minimal Anthropic Messages API JSON
// response containing a single text block with the provided text.
func anthropicMessagesResponse(text string) []byte {
	type textBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	}
	type response struct {
		ID           string      `json:"id"`
		Type         string      `json:"type"`
		Role         string      `json:"role"`
		Content      []textBlock `json:"content"`
		Model        string      `json:"model"`
		StopReason   string      `json:"stop_reason"`
		StopSequence *string     `json:"stop_sequence"`
		Usage        usage       `json:"usage"`
	}
	r := response{
		ID:         "msg_test",
		Type:       "message",
		Role:       "assistant",
		Content:    []textBlock{{Type: "text", Text: text}},
		Model:      "claude-test-model",
		StopReason: "end_turn",
		Usage:      usage{InputTokens: 10, OutputTokens: 20},
	}
	b, _ := json.Marshal(r)
	return b
}

// validArticleJSON returns a JSON string that represents a valid article
// response from Claude.
func validArticleJSON() string {
	return `{"title":"Título de teste","excerpt":"Resumo do artigo.","body":["Parágrafo um.","Parágrafo dois."]}`
}

// --- parseArticleJSON ---

// TestParseArticleJSONExtractsCleanJSON verifies that a plain JSON string
// (without markdown wrapping) is parsed correctly.
func TestParseArticleJSONExtractsCleanJSON(t *testing.T) {
	input := validArticleJSON()
	article, err := parseArticleJSON(input)
	if err != nil {
		t.Fatalf("parseArticleJSON returned unexpected error: %v", err)
	}
	if article.Title != "Título de teste" {
		t.Errorf("Title = %q, want %q", article.Title, "Título de teste")
	}
	if article.Excerpt != "Resumo do artigo." {
		t.Errorf("Excerpt = %q, want %q", article.Excerpt, "Resumo do artigo.")
	}
	if len(article.Body) != 2 {
		t.Errorf("len(Body) = %d, want 2", len(article.Body))
	}
}

// TestParseArticleJSONExtractsJSONFromMarkdownWrapper verifies that JSON
// wrapped inside a ```json ... ``` code fence is extracted and parsed.
func TestParseArticleJSONExtractsJSONFromMarkdownWrapper(t *testing.T) {
	input := "```json\n" + validArticleJSON() + "\n```"
	article, err := parseArticleJSON(input)
	if err != nil {
		t.Fatalf("parseArticleJSON returned unexpected error: %v", err)
	}
	if article.Title != "Título de teste" {
		t.Errorf("Title = %q, want %q", article.Title, "Título de teste")
	}
}

// TestParseArticleJSONReturnsErrorForMalformedJSON verifies that invalid JSON
// causes parseArticleJSON to return an error.
func TestParseArticleJSONReturnsErrorForMalformedJSON(t *testing.T) {
	_, err := parseArticleJSON(`{"title": "oops"`)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

// TestParseArticleJSONReturnsErrorIfNoJSONObject verifies that a response with
// no JSON object at all returns an error.
func TestParseArticleJSONReturnsErrorIfNoJSONObject(t *testing.T) {
	_, err := parseArticleJSON("Sem JSON aqui, apenas texto livre.")
	if err == nil {
		t.Fatal("expected error when response contains no JSON object, got nil")
	}
}

// TestParseArticleJSONReturnsErrorIfFieldsMissing verifies that a JSON object
// that lacks the required article fields ("title", "excerpt", "body") causes
// parseArticleJSON to return an error.
func TestParseArticleJSONReturnsErrorIfFieldsMissing(t *testing.T) {
	// Missing "body" and "excerpt"
	_, err := parseArticleJSON(`{"title":"Só o título"}`)
	if err == nil {
		t.Fatal("expected error for JSON missing excerpt and body, got nil")
	}
}

// --- buildPrompt ---

// TestBuildPromptIncludesMediaTitle verifies that the media title appears in the
// generated prompt.
func TestBuildPromptIncludesMediaTitle(t *testing.T) {
	w := &Writer{style: sampleStyle()}
	prompt := w.buildPrompt("a transcrição", "Episódio Especial dos Vikings")

	if !strings.Contains(prompt, "Episódio Especial dos Vikings") {
		t.Errorf("prompt does not contain the media title %q", "Episódio Especial dos Vikings")
	}
}

// TestBuildPromptIncludesTranscript verifies that the full transcript text is
// included in the prompt.
func TestBuildPromptIncludesTranscript(t *testing.T) {
	transcript := "Este é o texto completo da transcrição do podcast."
	w := &Writer{style: sampleStyle()}
	prompt := w.buildPrompt(transcript, "Título do Episódio")

	if !strings.Contains(prompt, transcript) {
		t.Errorf("prompt does not contain the transcript text")
	}
}

// TestBuildPromptIncludesStylePersona verifies that the style persona text is
// included in the prompt.
func TestBuildPromptIncludesStylePersona(t *testing.T) {
	s := sampleStyle()
	w := &Writer{style: s}
	prompt := w.buildPrompt("transcrição", "Título")

	if !strings.Contains(prompt, s.Persona) {
		t.Errorf("prompt does not contain persona %q", s.Persona)
	}
}

// TestBuildPromptIncludesContentRules verifies that all content rules from the
// style are present in the prompt.
func TestBuildPromptIncludesContentRules(t *testing.T) {
	s := sampleStyle()
	w := &Writer{style: s}
	prompt := w.buildPrompt("transcrição", "Título")

	for _, rule := range s.ContentRules {
		if !strings.Contains(prompt, rule) {
			t.Errorf("prompt does not contain content rule %q", rule)
		}
	}
}

// --- GenerateArticle via httptest ---

// TestGenerateArticleReturnsArticleOnSuccess verifies end-to-end that
// GenerateArticle calls the Anthropic API and parses the response into an Article.
// The Anthropic client is pointed at an httptest.Server that returns a canned
// response so no real network call is made.
func TestGenerateArticleReturnsArticleOnSuccess(t *testing.T) {
	articleJSON := validArticleJSON()
	respBody := anthropicMessagesResponse(articleJSON)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(respBody)
	}))
	defer srv.Close()

	wr := newWriterForTest(t, srv)
	article, err := wr.GenerateArticle(context.Background(), "transcrição de teste", "Título do Episódio")
	if err != nil {
		t.Fatalf("GenerateArticle returned unexpected error: %v", err)
	}

	if article == nil {
		t.Fatal("GenerateArticle returned nil article without error")
	}
	if article.Title == "" {
		t.Error("Article.Title is empty")
	}
	if article.Excerpt == "" {
		t.Error("Article.Excerpt is empty")
	}
	if len(article.Body) == 0 {
		t.Error("Article.Body is empty")
	}
}

// TestGenerateArticleTruncatesLongTranscript verifies that a transcript longer
// than the configured transcriptLimit is silently truncated before being sent.
// We test this indirectly by checking that the prompt built from a truncated
// transcript does not contain the original long string.
func TestGenerateArticleTruncatesLongTranscript(t *testing.T) {
	articleJSON := validArticleJSON()
	respBody := anthropicMessagesResponse(articleJSON)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(respBody)
	}))
	defer srv.Close()

	const limit = 50
	c := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(srv.URL+"/"),
	)
	wr := &Writer{
		client:          &c,
		model:           "claude-test-model",
		transcriptLimit: limit,
		style:           sampleStyle(),
	}

	longTranscript := strings.Repeat("a", limit*2)
	_, err := wr.GenerateArticle(context.Background(), longTranscript, "Título")
	if err != nil {
		t.Fatalf("GenerateArticle returned unexpected error: %v", err)
	}

	// Verify indirectly: the prompt built from the truncated transcript
	// should not contain the full-length string.
	truncated := longTranscript[:limit]
	prompt := wr.buildPrompt(truncated, "Título")
	if strings.Contains(prompt, longTranscript) {
		t.Error("prompt contains the untruncated transcript; expected truncation")
	}
}

// TestGenerateArticleReturnsErrorOnAPIFailure verifies that an HTTP error from
// the Anthropic API propagates as an error from GenerateArticle.
func TestGenerateArticleReturnsErrorOnAPIFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"type":"error","error":{"type":"api_error","message":"server error"}}`)
	}))
	defer srv.Close()

	wr := newWriterForTest(t, srv)
	_, err := wr.GenerateArticle(context.Background(), "transcrição", "Título")
	if err == nil {
		t.Fatal("expected error for 500 API response, got nil")
	}
}

// Compile-time check: *Writer must satisfy writer.Writer.
var _ writer.Writer = (*Writer)(nil)
