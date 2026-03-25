package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mguilhermetavares/odin-writer/internal/writer"
)

func TestSaveTranscript_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	err := m.SaveTranscript("media1", "hello transcript")
	if err != nil {
		t.Fatalf("SaveTranscript returned error: %v", err)
	}

	path := filepath.Join(dir, "media1", "transcript.txt")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("expected file %s to exist, but it does not", path)
	}
}

func TestLoadTranscript_ReturnsStoredContent(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	want := "this is the transcript content"
	if err := m.SaveTranscript("media1", want); err != nil {
		t.Fatalf("SaveTranscript: %v", err)
	}

	got, err := m.LoadTranscript("media1")
	if err != nil {
		t.Fatalf("LoadTranscript returned error: %v", err)
	}
	if got != want {
		t.Errorf("LoadTranscript = %q; want %q", got, want)
	}
}

func TestLoadTranscript_ReturnsEmptyIfNotFound(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	got, err := m.LoadTranscript("nonexistent")
	if err != nil {
		t.Fatalf("LoadTranscript returned unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("LoadTranscript = %q; want empty string", got)
	}
}

func TestSaveTranscript_OverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	if err := m.SaveTranscript("media1", "first content"); err != nil {
		t.Fatalf("SaveTranscript (first): %v", err)
	}
	if err := m.SaveTranscript("media1", "second content"); err != nil {
		t.Fatalf("SaveTranscript (second): %v", err)
	}

	got, err := m.LoadTranscript("media1")
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if got != "second content" {
		t.Errorf("LoadTranscript = %q; want %q", got, "second content")
	}
}

func TestSaveArticle_SerializesJSONCorrectly(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	article := &writer.Article{
		Title:   "Test Title",
		Excerpt: "Test Excerpt",
		Body:    []string{"paragraph one", "paragraph two"},
	}

	if err := m.SaveArticle("media1", article); err != nil {
		t.Fatalf("SaveArticle: %v", err)
	}

	path := filepath.Join(dir, "media1", "article.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var got writer.Article
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if got.Title != article.Title {
		t.Errorf("Title = %q; want %q", got.Title, article.Title)
	}
	if got.Excerpt != article.Excerpt {
		t.Errorf("Excerpt = %q; want %q", got.Excerpt, article.Excerpt)
	}
	if len(got.Body) != len(article.Body) {
		t.Fatalf("Body len = %d; want %d", len(got.Body), len(article.Body))
	}
	for i, p := range article.Body {
		if got.Body[i] != p {
			t.Errorf("Body[%d] = %q; want %q", i, got.Body[i], p)
		}
	}
}

func TestLoadArticle_ReturnsStoredArticle(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	want := &writer.Article{
		Title:   "Loaded Article",
		Excerpt: "Short excerpt",
		Body:    []string{"body line"},
	}

	if err := m.SaveArticle("media1", want); err != nil {
		t.Fatalf("SaveArticle: %v", err)
	}

	got, err := m.LoadArticle("media1")
	if err != nil {
		t.Fatalf("LoadArticle returned error: %v", err)
	}
	if got == nil {
		t.Fatal("LoadArticle returned nil; want non-nil")
	}
	if got.Title != want.Title {
		t.Errorf("Title = %q; want %q", got.Title, want.Title)
	}
	if got.Excerpt != want.Excerpt {
		t.Errorf("Excerpt = %q; want %q", got.Excerpt, want.Excerpt)
	}
}

func TestLoadArticle_ReturnsNilIfNotFound(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	got, err := m.LoadArticle("nonexistent")
	if err != nil {
		t.Fatalf("LoadArticle returned unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("LoadArticle = %v; want nil", got)
	}
}

func TestList_ReturnsIDsWithExistingCache(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	for _, id := range []string{"alpha", "beta", "gamma"} {
		if err := m.SaveTranscript(id, "content"); err != nil {
			t.Fatalf("SaveTranscript(%s): %v", id, err)
		}
	}

	ids, err := m.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("List returned %d IDs; want 3: %v", len(ids), ids)
	}

	found := make(map[string]bool)
	for _, id := range ids {
		found[id] = true
	}
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !found[want] {
			t.Errorf("List does not contain %q", want)
		}
	}
}

func TestList_ReturnsEmptyIfCacheEmpty(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "empty-cache"))

	ids, err := m.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("List returned %d IDs; want 0", len(ids))
	}
}

func TestClear_RemovesCacheForSpecificID(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)

	if err := m.SaveTranscript("keep", "content"); err != nil {
		t.Fatalf("SaveTranscript(keep): %v", err)
	}
	if err := m.SaveTranscript("remove", "content"); err != nil {
		t.Fatalf("SaveTranscript(remove): %v", err)
	}

	if err := m.Clear("remove"); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	ids, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, id := range ids {
		if id == "remove" {
			t.Errorf("List still contains %q after Clear", "remove")
		}
	}

	// "keep" must still be there
	found := false
	for _, id := range ids {
		if id == "keep" {
			found = true
		}
	}
	if !found {
		t.Errorf("List no longer contains %q; should be intact", "keep")
	}
}

func TestClearAll_RemovesEntireCache(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	m := New(cacheDir)

	for _, id := range []string{"a", "b", "c"} {
		if err := m.SaveTranscript(id, "content"); err != nil {
			t.Fatalf("SaveTranscript(%s): %v", id, err)
		}
	}

	if err := m.ClearAll(); err != nil {
		t.Fatalf("ClearAll: %v", err)
	}

	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Errorf("cache directory still exists after ClearAll")
	}

	// List on a non-existent dir should return empty without error
	ids, err := m.List()
	if err != nil {
		t.Fatalf("List after ClearAll returned error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("List after ClearAll returned %d IDs; want 0", len(ids))
	}
}
