package sanity

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"odin-writer/internal/writer"
)

// newPublisherForTest creates a Publisher wired to the given test server URL.
// It replaces the default Sanity API host by overriding the http.Client's
// transport with a redirect transport that rewrites the host to the test server.
func newPublisherForTest(t *testing.T, srv *httptest.Server) *Publisher {
	t.Helper()
	p := &Publisher{
		projectID: "testproject",
		dataset:   "testdataset",
		token:     "test-token-abc",
		client: &http.Client{
			Transport: &hostOverrideTransport{
				base:        http.DefaultTransport,
				targetURL:   srv.URL,
			},
		},
	}
	return p
}

// hostOverrideTransport rewrites every request so that it goes to targetURL
// instead of its original host. This lets us intercept calls to
// https://<projectID>.api.sanity.io without DNS tricks.
type hostOverrideTransport struct {
	base      http.RoundTripper
	targetURL string
}

func (t *hostOverrideTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request so we do not mutate the original.
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = strings.TrimPrefix(t.targetURL, "http://")
	return t.base.RoundTrip(clone)
}

// sampleArticle returns a minimal Article suitable for testing.
func sampleArticle() *writer.Article {
	return &writer.Article{
		Title:   "Vikings vencem por placar elástico",
		Excerpt: "Um resumo do jogo.",
		Body:    []string{"Primeiro parágrafo.", "Segundo parágrafo."},
	}
}

// TestCreateDraftSendsPostToCorrectSanityURL verifies that CreateDraft issues a
// POST request to the Sanity Mutations API path for the configured project and dataset.
func TestCreateDraftSendsPostToCorrectSanityURL(t *testing.T) {
	var capturedPath string
	var capturedMethod string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedMethod = r.Method
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	p := newPublisherForTest(t, srv)
	_, err := p.CreateDraft(context.Background(), sampleArticle(), "media-001")
	if err != nil {
		t.Fatalf("CreateDraft returned unexpected error: %v", err)
	}

	wantPath := "/v2021-06-07/data/mutate/testdataset"
	if capturedPath != wantPath {
		t.Errorf("request path = %q, want %q", capturedPath, wantPath)
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("request method = %q, want POST", capturedMethod)
	}
}

// TestCreateDraftIncludesAuthorizationHeader verifies that the Bearer token is
// sent in the Authorization header.
func TestCreateDraftIncludesAuthorizationHeader(t *testing.T) {
	var capturedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	p := newPublisherForTest(t, srv)
	_, err := p.CreateDraft(context.Background(), sampleArticle(), "media-001")
	if err != nil {
		t.Fatalf("CreateDraft returned unexpected error: %v", err)
	}

	wantAuth := "Bearer test-token-abc"
	if capturedAuth != wantAuth {
		t.Errorf("Authorization header = %q, want %q", capturedAuth, wantAuth)
	}
}

// TestCreateDraftReturnsCorrectDocID verifies that CreateDraft returns the
// expected document ID ("drafts.odin-writer-<mediaID>").
func TestCreateDraftReturnsCorrectDocID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	p := newPublisherForTest(t, srv)
	docID, err := p.CreateDraft(context.Background(), sampleArticle(), "media-xyz")
	if err != nil {
		t.Fatalf("CreateDraft returned unexpected error: %v", err)
	}

	wantDocID := "drafts.odin-writer-media-xyz"
	if docID != wantDocID {
		t.Errorf("docID = %q, want %q", docID, wantDocID)
	}
}

// TestCreateDraftReturnsErrorOn4xx verifies that a 4xx HTTP response causes
// CreateDraft to return a non-nil error.
func TestCreateDraftReturnsErrorOn4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	p := newPublisherForTest(t, srv)
	_, err := p.CreateDraft(context.Background(), sampleArticle(), "media-001")
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
}

// TestCreateDraftReturnsErrorOn5xx verifies that a 5xx HTTP response causes
// CreateDraft to return a non-nil error.
func TestCreateDraftReturnsErrorOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer srv.Close()

	p := newPublisherForTest(t, srv)
	// Disable retry by using a plain http.Client without the RetryTransport.
	p.client = &http.Client{
		Transport: &hostOverrideTransport{
			base:      http.DefaultTransport,
			targetURL: srv.URL,
		},
	}
	_, err := p.CreateDraft(context.Background(), sampleArticle(), "media-001")
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

// TestCreateDraftSendsMutationPayload verifies that the request body contains a
// valid "mutations" array with a "createOrReplace" key.
func TestCreateDraftSendsMutationPayload(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	p := newPublisherForTest(t, srv)
	_, err := p.CreateDraft(context.Background(), sampleArticle(), "media-001")
	if err != nil {
		t.Fatalf("CreateDraft returned unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}
	mutations, ok := payload["mutations"]
	if !ok {
		t.Fatal("payload missing 'mutations' key")
	}
	list, ok := mutations.([]any)
	if !ok || len(list) == 0 {
		t.Fatal("'mutations' is not a non-empty array")
	}
	first, ok := list[0].(map[string]any)
	if !ok {
		t.Fatal("first mutation is not an object")
	}
	if _, ok := first["createOrReplace"]; !ok {
		t.Error("first mutation does not contain 'createOrReplace' key")
	}
}

// --- slugify ---

// TestSlugifyConvertsAccentedTitleToASCII verifies the canonical example from
// the task: "Análise" → "analise".
func TestSlugifyConvertsAccentedTitleToASCII(t *testing.T) {
	got := slugify("Análise")
	want := "analise"
	if got != want {
		t.Errorf("slugify(%q) = %q, want %q", "Análise", got, want)
	}
}

// TestSlugifyConvertsSpacesToHyphens verifies that spaces become hyphens.
func TestSlugifyConvertsSpacesToHyphens(t *testing.T) {
	got := slugify("hello world")
	want := "hello-world"
	if got != want {
		t.Errorf("slugify(%q) = %q, want %q", "hello world", got, want)
	}
}

// TestSlugifyConvertsToLowercase verifies that uppercase letters are lowercased.
func TestSlugifyConvertsToLowercase(t *testing.T) {
	got := slugify("Vikings WIN")
	want := "vikings-win"
	if got != want {
		t.Errorf("slugify(%q) = %q, want %q", "Vikings WIN", got, want)
	}
}

// TestSlugifyRemovesSpecialCharacters verifies that punctuation and symbols are
// stripped from the slug.
func TestSlugifyRemovesSpecialCharacters(t *testing.T) {
	got := slugify("NFL 2024: top 10!")
	want := "nfl-2024-top-10"
	if got != want {
		t.Errorf("slugify(%q) = %q, want %q", "NFL 2024: top 10!", got, want)
	}
}

// --- paragraphsToPortableText ---

// TestParagraphsToPortableTextProducesBlockType verifies that each non-empty
// paragraph yields a block with _type == "block".
func TestParagraphsToPortableTextProducesBlockType(t *testing.T) {
	paragraphs := []string{"Parágrafo um.", "Parágrafo dois."}
	blocks := paragraphsToPortableText(paragraphs)

	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d", len(blocks))
	}
	for i, b := range blocks {
		if b.Type != "block" {
			t.Errorf("blocks[%d]._type = %q, want %q", i, b.Type, "block")
		}
	}
}

// TestParagraphsToPortableTextIgnoresEmptyParagraphs verifies that empty or
// whitespace-only strings are not converted into blocks.
func TestParagraphsToPortableTextIgnoresEmptyParagraphs(t *testing.T) {
	paragraphs := []string{"Válido.", "", "   ", "Também válido."}
	blocks := paragraphsToPortableText(paragraphs)

	if len(blocks) != 2 {
		t.Errorf("want 2 blocks (empty paragraphs skipped), got %d", len(blocks))
	}
}

// TestParagraphsToPortableTextBlocksHaveNonEmptyKeys verifies that every block
// and its first child span have a non-empty _key.
func TestParagraphsToPortableTextBlocksHaveNonEmptyKeys(t *testing.T) {
	blocks := paragraphsToPortableText([]string{"Um.", "Dois.", "Três."})

	for i, b := range blocks {
		if b.Key == "" {
			t.Errorf("blocks[%d]._key is empty", i)
		}
		if len(b.Children) == 0 {
			t.Errorf("blocks[%d] has no children", i)
			continue
		}
		if b.Children[0].Key == "" {
			t.Errorf("blocks[%d].children[0]._key is empty", i)
		}
	}
}
