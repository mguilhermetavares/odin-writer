package server

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mguilhermetavares/odin-writer/internal/cache"
	"github.com/mguilhermetavares/odin-writer/internal/pipeline"
	"github.com/mguilhermetavares/odin-writer/internal/source"
	"github.com/mguilhermetavares/odin-writer/internal/state"
	"github.com/mguilhermetavares/odin-writer/internal/writer"
)

// ---------------------------------------------------------------------------
// Mock source / transcriber / writer / publisher
// ---------------------------------------------------------------------------

type countingSource struct {
	calls atomic.Int64
	media *source.Media
	err   error
}

func (s *countingSource) Prepare(_ context.Context, _ source.Options, _ string) (*source.Media, error) {
	s.calls.Add(1)
	return s.media, s.err
}

type noopTranscriber struct{}

func (t *noopTranscriber) Transcribe(_ context.Context, _ string, _ int) (string, error) {
	return "transcript", nil
}

type noopWriter struct{}

func (w *noopWriter) GenerateArticle(_ context.Context, _, _ string) (*writer.Article, error) {
	return &writer.Article{Title: "T", Excerpt: "E"}, nil
}

type noopPublisher struct{}

func (p *noopPublisher) CreateDraft(_ context.Context, _ *writer.Article, _ string) (string, error) {
	return "doc-id", nil
}

type failingPublisher struct {
	err error
}

func (p *failingPublisher) CreateDraft(_ context.Context, _ *writer.Article, _ string) (string, error) {
	return "", p.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func successMedia(id string) *source.Media {
	return &source.Media{
		ID:          id,
		Title:       "Video " + id,
		AudioPath:   "/tmp/fake.m4a",
		SourceID:    "youtube",
		DurationSec: 60,
	}
}

func newRunner(t *testing.T, src source.Source, pub interface {
	CreateDraft(context.Context, *writer.Article, string) (string, error)
}) *pipeline.Runner {
	t.Helper()
	dir := t.TempDir()
	c := cache.New(filepath.Join(dir, "cache"))
	s := state.New(filepath.Join(dir, "state.json"))
	return pipeline.NewRunner(src, &noopTranscriber{}, &noopWriter{}, pub, c, s)
}

// Tests are in server_synctest_test.go (use testing/synctest for fake time).
