package localfile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"odin-writer/internal/source"
)

func newSource() *Source {
	return New()
}

// writeTempFile creates a temporary file inside dir with the given name and
// content and returns its absolute path.
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing temp file %s: %v", path, err)
	}
	return path
}

// TestPrepareValidFileReturnsPopulatedMedia verifies that a valid file path
// produces a Media with non-empty ID, Title, and AudioPath fields.
func TestPrepareValidFileReturnsPopulatedMedia(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "episode.mp3", "audio data")

	s := newSource()
	media, err := s.Prepare(context.Background(), source.Options{Path: path}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if media.ID == "" {
		t.Error("expected non-empty ID")
	}
	if media.Title == "" {
		t.Error("expected non-empty Title")
	}
	if media.AudioPath == "" {
		t.Error("expected non-empty AudioPath")
	}
	if media.AudioPath != path {
		t.Errorf("AudioPath: want %q, got %q", path, media.AudioPath)
	}
}

// TestPrepareFailsWhenFileDoesNotExist verifies that a non-existent path
// produces an error.
func TestPrepareFailsWhenFileDoesNotExist(t *testing.T) {
	s := newSource()
	_, err := s.Prepare(context.Background(), source.Options{Path: "/nonexistent/path/audio.mp3"}, "")
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

// TestPrepareFailsWhenPathIsDirectory verifies that passing a directory path
// returns an error.
func TestPrepareFailsWhenPathIsDirectory(t *testing.T) {
	dir := t.TempDir()

	s := newSource()
	_, err := s.Prepare(context.Background(), source.Options{Path: dir}, "")
	if err == nil {
		t.Errorf("expected error when path is a directory, got nil")
	}
}

// TestPrepareFailsWhenPathIsEmpty verifies that an empty opts.Path returns an
// error.
func TestPrepareFailsWhenPathIsEmpty(t *testing.T) {
	s := newSource()
	_, err := s.Prepare(context.Background(), source.Options{Path: ""}, "")
	if err == nil {
		t.Error("expected error for empty path, got nil")
	}
}

// TestTitleDefaultsToFilenameWithoutExtension verifies that when opts.Title is
// not set the title is derived from the filename minus its extension.
func TestTitleDefaultsToFilenameWithoutExtension(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "my-episode.mp3", "audio")

	s := newSource()
	media, err := s.Prepare(context.Background(), source.Options{Path: path}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "my-episode"
	if media.Title != want {
		t.Errorf("Title: want %q, got %q", want, media.Title)
	}
}

// TestTitleOverrideViaOpts verifies that opts.Title takes precedence over the
// filename-derived default.
func TestTitleOverrideViaOpts(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "episode.mp3", "audio")

	s := newSource()
	media, err := s.Prepare(context.Background(), source.Options{Path: path, Title: "Custom Title"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if media.Title != "Custom Title" {
		t.Errorf("Title: want %q, got %q", "Custom Title", media.Title)
	}
}

// TestSourceIDIsAlwaysLocalfile verifies that SourceID is always "localfile"
// regardless of the file path.
func TestSourceIDIsAlwaysLocalfile(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "audio.wav", "wav data")

	s := newSource()
	media, err := s.Prepare(context.Background(), source.Options{Path: path}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if media.SourceID != "localfile" {
		t.Errorf("SourceID: want %q, got %q", "localfile", media.SourceID)
	}
}

// TestFileIDIsStableForSameFile verifies that repeated calls for the same file
// produce the same ID.
func TestFileIDIsStableForSameFile(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "stable.mp3", "consistent content")

	s := newSource()

	media1, err := s.Prepare(context.Background(), source.Options{Path: path}, "")
	if err != nil {
		t.Fatalf("first Prepare: %v", err)
	}
	media2, err := s.Prepare(context.Background(), source.Options{Path: path}, "")
	if err != nil {
		t.Fatalf("second Prepare: %v", err)
	}

	if media1.ID != media2.ID {
		t.Errorf("ID should be stable: first=%q, second=%q", media1.ID, media2.ID)
	}
}

// TestFileIDDiffersForDifferentFiles verifies that two distinct files with
// different content produce different IDs.
func TestFileIDDiffersForDifferentFiles(t *testing.T) {
	dir := t.TempDir()
	pathA := writeTempFile(t, dir, "fileA.mp3", "content for file A")
	pathB := writeTempFile(t, dir, "fileB.mp3", "content for file B — different")

	s := newSource()

	mediaA, err := s.Prepare(context.Background(), source.Options{Path: pathA}, "")
	if err != nil {
		t.Fatalf("Prepare fileA: %v", err)
	}
	mediaB, err := s.Prepare(context.Background(), source.Options{Path: pathB}, "")
	if err != nil {
		t.Fatalf("Prepare fileB: %v", err)
	}

	if mediaA.ID == mediaB.ID {
		t.Errorf("expected different IDs for different files, both got %q", mediaA.ID)
	}
}
