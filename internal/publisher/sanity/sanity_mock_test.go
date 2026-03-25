package sanity

// Mock Sanity Mutations API server with full document validation.
//
// The real Sanity API expects:
//   POST /v2021-06-07/data/mutate/{dataset}
//   Content-Type: application/json
//   Authorization: Bearer <token>
//   Body: {"mutations":[{"createOrReplace":{...document...}}]}
//
// The tests below verify that the published document contains every field that
// the Sanity schema expects, not just that the "mutations" key is present.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// capturedMutation holds the decoded createOrReplace document.
type capturedMutation struct {
	contentType string
	authHeader  string
	doc         map[string]any
}

// sanityMockServer is an httptest.Server that captures the full mutation doc.
type sanityMockServer struct {
	srv      *httptest.Server
	Captured capturedMutation
	code     int
}

func newSanityMock(t *testing.T, code int) *sanityMockServer {
	t.Helper()
	m := &sanityMockServer{code: code}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Captured.contentType = r.Header.Get("Content-Type")
		m.Captured.authHeader = r.Header.Get("Authorization")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var payload struct {
			Mutations []struct {
				CreateOrReplace map[string]any `json:"createOrReplace"`
			} `json:"mutations"`
		}
		if err := json.Unmarshal(body, &payload); err == nil &&
			len(payload.Mutations) > 0 {
			m.Captured.doc = payload.Mutations[0].CreateOrReplace
		}

		w.WriteHeader(m.code)
		w.Write([]byte(`{}`))
	}))
	return m
}

// publisherFor returns a Publisher wired to the mock server.
func publisherFor(t *testing.T, m *sanityMockServer) *Publisher {
	t.Helper()
	return &Publisher{
		projectID: "testproject",
		dataset:   "testdataset",
		token:     "test-token",
		client: &http.Client{
			Transport: &hostOverrideTransport{
				base:      http.DefaultTransport,
				targetURL: m.srv.URL,
			},
		},
	}
}

// docField is a helper to extract a string field from the captured document.
func docField(doc map[string]any, key string) string {
	v, _ := doc[key].(string)
	return v
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestSanity_DocumentTypeIsArticle verifies that the _type field equals "article".
func TestSanity_DocumentTypeIsArticle(t *testing.T) {
	m := newSanityMock(t, http.StatusOK)
	defer m.srv.Close()

	p := publisherFor(t, m)
	if _, err := p.CreateDraft(context.Background(), sampleArticle(), "m1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := docField(m.Captured.doc, "_type"), "article"; got != want {
		t.Errorf("_type = %q, want %q", got, want)
	}
}

// TestSanity_DocumentTitleMatchesArticle verifies that title is copied from the
// Article struct.
func TestSanity_DocumentTitleMatchesArticle(t *testing.T) {
	m := newSanityMock(t, http.StatusOK)
	defer m.srv.Close()

	article := sampleArticle()
	p := publisherFor(t, m)
	if _, err := p.CreateDraft(context.Background(), article, "m1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := docField(m.Captured.doc, "title"); got != article.Title {
		t.Errorf("title = %q, want %q", got, article.Title)
	}
}

// TestSanity_DocumentExcerptMatchesArticle verifies the excerpt field.
func TestSanity_DocumentExcerptMatchesArticle(t *testing.T) {
	m := newSanityMock(t, http.StatusOK)
	defer m.srv.Close()

	article := sampleArticle()
	p := publisherFor(t, m)
	if _, err := p.CreateDraft(context.Background(), article, "m1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := docField(m.Captured.doc, "excerpt"); got != article.Excerpt {
		t.Errorf("excerpt = %q, want %q", got, article.Excerpt)
	}
}

// TestSanity_SlugIsSlugifiedTitle verifies that slug.current is the URL-safe
// version of the article title.
func TestSanity_SlugIsSlugifiedTitle(t *testing.T) {
	m := newSanityMock(t, http.StatusOK)
	defer m.srv.Close()

	article := sampleArticle() // "Vikings vencem por placar elástico"
	p := publisherFor(t, m)
	if _, err := p.CreateDraft(context.Background(), article, "m1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	slug, _ := m.Captured.doc["slug"].(map[string]any)
	if slug == nil {
		t.Fatal("slug field missing or not an object")
	}
	current, _ := slug["current"].(string)
	want := slugify(article.Title)
	if current != want {
		t.Errorf("slug.current = %q, want %q", current, want)
	}
}

// TestSanity_BodyIsPortableTextArray verifies that the body field is a
// non-empty array of Sanity Portable Text blocks.
func TestSanity_BodyIsPortableTextArray(t *testing.T) {
	m := newSanityMock(t, http.StatusOK)
	defer m.srv.Close()

	p := publisherFor(t, m)
	article := sampleArticle()
	if _, err := p.CreateDraft(context.Background(), article, "m1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body, ok := m.Captured.doc["body"].([]any)
	if !ok || len(body) == 0 {
		t.Fatalf("body is not a non-empty array, got %T", m.Captured.doc["body"])
	}
	if len(body) != len(article.Body) {
		t.Errorf("body has %d blocks, want %d", len(body), len(article.Body))
	}

	// Each block should have _type:"block" and a non-empty _key.
	for i, blk := range body {
		block, ok := blk.(map[string]any)
		if !ok {
			t.Errorf("body[%d] is not an object", i)
			continue
		}
		if typ, _ := block["_type"].(string); typ != "block" {
			t.Errorf("body[%d]._type = %q, want %q", i, typ, "block")
		}
		if key, _ := block["_key"].(string); key == "" {
			t.Errorf("body[%d]._key is empty", i)
		}
	}
}

// TestSanity_AuthorIsFixed verifies that author is always "Minnesota Vikings BR".
func TestSanity_AuthorIsFixed(t *testing.T) {
	m := newSanityMock(t, http.StatusOK)
	defer m.srv.Close()

	p := publisherFor(t, m)
	if _, err := p.CreateDraft(context.Background(), sampleArticle(), "m1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := docField(m.Captured.doc, "author"), "Minnesota Vikings BR"; got != want {
		t.Errorf("author = %q, want %q", got, want)
	}
}

// TestSanity_CategoryIsNoticias verifies that category is always "noticias".
func TestSanity_CategoryIsNoticias(t *testing.T) {
	m := newSanityMock(t, http.StatusOK)
	defer m.srv.Close()

	p := publisherFor(t, m)
	if _, err := p.CreateDraft(context.Background(), sampleArticle(), "m1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := docField(m.Captured.doc, "category"), "noticias"; got != want {
		t.Errorf("category = %q, want %q", got, want)
	}
}

// TestSanity_PublishedAtIsValidRFC3339 verifies that publishedAt is a valid
// RFC3339 timestamp.
func TestSanity_PublishedAtIsValidRFC3339(t *testing.T) {
	m := newSanityMock(t, http.StatusOK)
	defer m.srv.Close()

	p := publisherFor(t, m)
	if _, err := p.CreateDraft(context.Background(), sampleArticle(), "m1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw := docField(m.Captured.doc, "publishedAt")
	if raw == "" {
		t.Fatal("publishedAt is empty")
	}
	if _, err := time.Parse(time.RFC3339, raw); err != nil {
		t.Errorf("publishedAt %q is not RFC3339: %v", raw, err)
	}
}

// TestSanity_ContentTypeIsJSON verifies the Content-Type header.
func TestSanity_ContentTypeIsJSON(t *testing.T) {
	m := newSanityMock(t, http.StatusOK)
	defer m.srv.Close()

	p := publisherFor(t, m)
	if _, err := p.CreateDraft(context.Background(), sampleArticle(), "m1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasPrefix(m.Captured.contentType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", m.Captured.contentType)
	}
}

// TestSanity_DocumentIDIncludesMediaID verifies the _id field encodes mediaID.
func TestSanity_DocumentIDIncludesMediaID(t *testing.T) {
	m := newSanityMock(t, http.StatusOK)
	defer m.srv.Close()

	p := publisherFor(t, m)
	if _, err := p.CreateDraft(context.Background(), sampleArticle(), "video-abc"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	id := docField(m.Captured.doc, "_id")
	if !strings.Contains(id, "video-abc") {
		t.Errorf("_id = %q does not contain mediaID %q", id, "video-abc")
	}
	if !strings.HasPrefix(id, "drafts.") {
		t.Errorf("_id = %q does not start with %q", id, "drafts.")
	}
}
