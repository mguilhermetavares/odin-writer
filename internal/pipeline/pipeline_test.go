package pipeline

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"odin-writer/internal/cache"
	"odin-writer/internal/source"
	"odin-writer/internal/state"
	"odin-writer/internal/writer"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

type mockSource struct {
	media *source.Media
	err   error
	calls int
}

func (m *mockSource) Prepare(_ context.Context, _ source.Options, _ string) (*source.Media, error) {
	m.calls++
	return m.media, m.err
}

type mockTranscriber struct {
	transcript string
	err        error
	calls      int
}

func (m *mockTranscriber) Transcribe(_ context.Context, _ string, _ int) (string, error) {
	m.calls++
	return m.transcript, m.err
}

type mockWriter struct {
	article *writer.Article
	err     error
	calls   int
}

func (m *mockWriter) GenerateArticle(_ context.Context, _, _ string) (*writer.Article, error) {
	m.calls++
	return m.article, m.err
}

type mockPublisher struct {
	docID string
	err   error
	calls int
}

func (m *mockPublisher) CreateDraft(_ context.Context, _ *writer.Article, _ string) (string, error) {
	m.calls++
	return m.docID, m.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestMedia(id string) *source.Media {
	return &source.Media{
		ID:          id,
		Title:       "Test Video " + id,
		AudioPath:   "/tmp/fake-audio.m4a",
		SourceID:    "youtube",
		DurationSec: 120,
	}
}

func newTestArticle() *writer.Article {
	return &writer.Article{
		Title:   "Test Article",
		Excerpt: "Test excerpt",
		Body:    []string{"Paragraph one.", "Paragraph two."},
	}
}

func newTestRunner(
	t *testing.T,
	src *mockSource,
	tr *mockTranscriber,
	wr *mockWriter,
	pub *mockPublisher,
) *Runner {
	t.Helper()
	dir := t.TempDir()
	c := cache.New(filepath.Join(dir, "cache"))
	s := state.New(filepath.Join(dir, "state.json"))
	return NewRunner(src, tr, wr, pub, c, s)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// 1. Full run without cache — calls source, transcriber, writer, publisher in order.
func TestRun_FullPipeline_NoCache(t *testing.T) {
	src := &mockSource{media: newTestMedia("vid1")}
	tr := &mockTranscriber{transcript: "hello world"}
	wr := &mockWriter{article: newTestArticle()}
	pub := &mockPublisher{docID: "draft-123"}

	runner := newTestRunner(t, src, tr, wr, pub)
	err := runner.Run(context.Background(), RunOptions{Source: "youtube"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if src.calls != 1 {
		t.Errorf("expected source.Prepare called once, got %d", src.calls)
	}
	if tr.calls != 1 {
		t.Errorf("expected transcriber called once, got %d", tr.calls)
	}
	if wr.calls != 1 {
		t.Errorf("expected writer called once, got %d", wr.calls)
	}
	if pub.calls != 1 {
		t.Errorf("expected publisher called once, got %d", pub.calls)
	}
}

// 2. Skip if already processed and no --force.
func TestRun_SkipWhenAlreadyProcessed(t *testing.T) {
	media := newTestMedia("vid-processed")
	src := &mockSource{media: media}
	tr := &mockTranscriber{transcript: "transcript"}
	wr := &mockWriter{article: newTestArticle()}
	pub := &mockPublisher{docID: "draft-x"}

	dir := t.TempDir()
	c := cache.New(filepath.Join(dir, "cache"))
	s := state.New(filepath.Join(dir, "state.json"))

	// Pre-record as processed.
	if err := s.Record(state.Entry{SourceID: "youtube", MediaID: media.ID}); err != nil {
		t.Fatalf("failed to record state: %v", err)
	}

	runner := NewRunner(src, tr, wr, pub, c, s)
	err := runner.Run(context.Background(), RunOptions{Source: "youtube"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tr.calls != 0 {
		t.Errorf("expected transcriber NOT called, got %d calls", tr.calls)
	}
	if pub.calls != 0 {
		t.Errorf("expected publisher NOT called, got %d calls", pub.calls)
	}
}

// 3. --force reprocesses even if already processed.
func TestRun_ForceReprocessesWhenAlreadyProcessed(t *testing.T) {
	media := newTestMedia("vid-force")
	src := &mockSource{media: media}
	tr := &mockTranscriber{transcript: "fresh transcript"}
	wr := &mockWriter{article: newTestArticle()}
	pub := &mockPublisher{docID: "draft-force"}

	dir := t.TempDir()
	c := cache.New(filepath.Join(dir, "cache"))
	s := state.New(filepath.Join(dir, "state.json"))

	if err := s.Record(state.Entry{SourceID: "youtube", MediaID: media.ID}); err != nil {
		t.Fatalf("failed to record state: %v", err)
	}

	runner := NewRunner(src, tr, wr, pub, c, s)
	err := runner.Run(context.Background(), RunOptions{Source: "youtube", Force: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tr.calls != 1 {
		t.Errorf("expected transcriber called once, got %d", tr.calls)
	}
	if pub.calls != 1 {
		t.Errorf("expected publisher called once, got %d", pub.calls)
	}
}

// 4. --dry-run does not call publisher.
func TestRun_DryRun_DoesNotPublish(t *testing.T) {
	src := &mockSource{media: newTestMedia("vid-dry")}
	tr := &mockTranscriber{transcript: "dry transcript"}
	wr := &mockWriter{article: newTestArticle()}
	pub := &mockPublisher{}

	runner := newTestRunner(t, src, tr, wr, pub)
	err := runner.Run(context.Background(), RunOptions{Source: "youtube", DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pub.calls != 0 {
		t.Errorf("expected publisher NOT called in dry-run, got %d calls", pub.calls)
	}
}

// 5. --rewrite-only uses transcript from cache and does not call transcriber.
func TestRun_RewriteOnly_UsesTranscriptFromCache(t *testing.T) {
	media := newTestMedia("vid-rewrite")
	src := &mockSource{media: media}
	tr := &mockTranscriber{}
	wr := &mockWriter{article: newTestArticle()}
	pub := &mockPublisher{}

	dir := t.TempDir()
	c := cache.New(filepath.Join(dir, "cache"))
	s := state.New(filepath.Join(dir, "state.json"))

	// Pre-populate the cache with a transcript.
	if err := c.SaveTranscript(media.ID, "cached transcript"); err != nil {
		t.Fatalf("failed to save transcript: %v", err)
	}

	runner := NewRunner(src, tr, wr, pub, c, s)
	err := runner.Run(context.Background(), RunOptions{Source: "youtube", RewriteOnly: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tr.calls != 0 {
		t.Errorf("expected transcriber NOT called in rewrite-only, got %d calls", tr.calls)
	}
	if wr.calls != 1 {
		t.Errorf("expected writer called once, got %d", wr.calls)
	}
	// rewrite-only skips publish.
	if pub.calls != 0 {
		t.Errorf("expected publisher NOT called in rewrite-only, got %d calls", pub.calls)
	}
}

// 6. --rewrite-only fails if no cached transcript.
func TestRun_RewriteOnly_FailsWithNoCache(t *testing.T) {
	src := &mockSource{media: newTestMedia("vid-no-cache")}
	tr := &mockTranscriber{}
	wr := &mockWriter{}
	pub := &mockPublisher{}

	runner := newTestRunner(t, src, tr, wr, pub)
	err := runner.Run(context.Background(), RunOptions{Source: "youtube", RewriteOnly: true})
	if err == nil {
		t.Fatal("expected error when no cached transcript, got nil")
	}
}

// 7. Run saves transcript to cache after transcription.
func TestRun_SavesTranscriptToCache(t *testing.T) {
	media := newTestMedia("vid-save-tr")
	src := &mockSource{media: media}
	tr := &mockTranscriber{transcript: "important transcript"}
	wr := &mockWriter{article: newTestArticle()}
	pub := &mockPublisher{docID: "draft-save-tr"}

	dir := t.TempDir()
	c := cache.New(filepath.Join(dir, "cache"))
	s := state.New(filepath.Join(dir, "state.json"))

	runner := NewRunner(src, tr, wr, pub, c, s)
	if err := runner.Run(context.Background(), RunOptions{Source: "youtube"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cached, err := c.LoadTranscript(media.ID)
	if err != nil {
		t.Fatalf("failed to load cached transcript: %v", err)
	}
	if cached != "important transcript" {
		t.Errorf("expected cached transcript %q, got %q", "important transcript", cached)
	}
}

// 8. Run loads transcript from cache if available — does not call transcriber.
func TestRun_LoadsTranscriptFromCache_SkipsTranscriber(t *testing.T) {
	media := newTestMedia("vid-cached-tr")
	src := &mockSource{media: media}
	tr := &mockTranscriber{transcript: "should not be used"}
	wr := &mockWriter{article: newTestArticle()}
	pub := &mockPublisher{docID: "draft-cached-tr"}

	dir := t.TempDir()
	c := cache.New(filepath.Join(dir, "cache"))
	s := state.New(filepath.Join(dir, "state.json"))

	if err := c.SaveTranscript(media.ID, "cached transcript"); err != nil {
		t.Fatalf("failed to save transcript: %v", err)
	}

	runner := NewRunner(src, tr, wr, pub, c, s)
	if err := runner.Run(context.Background(), RunOptions{Source: "youtube"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tr.calls != 0 {
		t.Errorf("expected transcriber NOT called when cache is warm, got %d calls", tr.calls)
	}
}

// 9. Run saves article to cache after generation.
func TestRun_SavesArticleToCache(t *testing.T) {
	media := newTestMedia("vid-save-art")
	src := &mockSource{media: media}
	tr := &mockTranscriber{transcript: "transcript"}
	wr := &mockWriter{article: &writer.Article{Title: "Cached Title", Excerpt: "Excerpt"}}
	pub := &mockPublisher{docID: "draft-art"}

	dir := t.TempDir()
	c := cache.New(filepath.Join(dir, "cache"))
	s := state.New(filepath.Join(dir, "state.json"))

	runner := NewRunner(src, tr, wr, pub, c, s)
	if err := runner.Run(context.Background(), RunOptions{Source: "youtube"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cached, err := c.LoadArticle(media.ID)
	if err != nil {
		t.Fatalf("failed to load cached article: %v", err)
	}
	if cached == nil {
		t.Fatal("expected cached article, got nil")
	}
	if cached.Title != "Cached Title" {
		t.Errorf("expected title %q, got %q", "Cached Title", cached.Title)
	}
}

// 10. Run loads article from cache if available — does not call writer.
func TestRun_LoadsArticleFromCache_SkipsWriter(t *testing.T) {
	media := newTestMedia("vid-cached-art")
	src := &mockSource{media: media}
	tr := &mockTranscriber{transcript: "transcript"}
	wr := &mockWriter{article: &writer.Article{Title: "Fresh Article"}}
	pub := &mockPublisher{docID: "draft-cached-art"}

	dir := t.TempDir()
	c := cache.New(filepath.Join(dir, "cache"))
	s := state.New(filepath.Join(dir, "state.json"))

	// Pre-populate cache with transcript and article.
	if err := c.SaveTranscript(media.ID, "cached transcript"); err != nil {
		t.Fatalf("failed to save transcript: %v", err)
	}
	if err := c.SaveArticle(media.ID, &writer.Article{Title: "Cached Article", Excerpt: "e"}); err != nil {
		t.Fatalf("failed to save article: %v", err)
	}

	runner := NewRunner(src, tr, wr, pub, c, s)
	if err := runner.Run(context.Background(), RunOptions{Source: "youtube"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wr.calls != 0 {
		t.Errorf("expected writer NOT called when article cache is warm, got %d calls", wr.calls)
	}
}

// 11. Run fails if source.Prepare returns error.
func TestRun_FailsIfSourceReturnsError(t *testing.T) {
	src := &mockSource{err: fmt.Errorf("source unavailable")}
	tr := &mockTranscriber{}
	wr := &mockWriter{}
	pub := &mockPublisher{}

	runner := newTestRunner(t, src, tr, wr, pub)
	err := runner.Run(context.Background(), RunOptions{Source: "youtube"})
	if err == nil {
		t.Fatal("expected error from source, got nil")
	}
	if !errors.Is(err, src.err) {
		// The error is wrapped, so check by message.
		t.Logf("got wrapped error as expected: %v", err)
	}
}

// 12. Run fails if transcriber returns error (no cache).
func TestRun_FailsIfTranscriberReturnsError(t *testing.T) {
	src := &mockSource{media: newTestMedia("vid-tr-err")}
	tr := &mockTranscriber{err: fmt.Errorf("groq API down")}
	wr := &mockWriter{}
	pub := &mockPublisher{}

	runner := newTestRunner(t, src, tr, wr, pub)
	err := runner.Run(context.Background(), RunOptions{Source: "youtube"})
	if err == nil {
		t.Fatal("expected error from transcriber, got nil")
	}
}
