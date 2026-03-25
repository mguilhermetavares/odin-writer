package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mguilhermetavares/odin-writer/internal/writer"
)

// Manager handles reading and writing cached transcripts and articles.
type Manager struct {
	dir string
}

func New(dir string) *Manager {
	return &Manager{dir: dir}
}

func (m *Manager) mediaDir(mediaID string) string {
	return filepath.Join(m.dir, mediaID)
}

// LoadTranscript returns the cached transcript for mediaID, or ("", nil) if not cached.
func (m *Manager) LoadTranscript(mediaID string) (string, error) {
	path := filepath.Join(m.mediaDir(mediaID), "transcript.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("reading cached transcript: %w", err)
	}
	return string(data), nil
}

// SaveTranscript writes the transcript to cache.
func (m *Manager) SaveTranscript(mediaID, transcript string) error {
	dir := m.mediaDir(mediaID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "transcript.txt"), []byte(transcript), 0o644)
}

// LoadArticle returns the cached article for mediaID, or (nil, nil) if not cached.
func (m *Manager) LoadArticle(mediaID string) (*writer.Article, error) {
	path := filepath.Join(m.mediaDir(mediaID), "article.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading cached article: %w", err)
	}

	var a writer.Article
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("parsing cached article: %w", err)
	}
	return &a, nil
}

// SaveArticle writes the article to cache.
func (m *Manager) SaveArticle(mediaID string, article *writer.Article) error {
	dir := m.mediaDir(mediaID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(article, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "article.json"), data, 0o644)
}

// List returns all media IDs that have a cache directory.
func (m *Manager) List() ([]string, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// Clear removes the cache directory for the given mediaID.
func (m *Manager) Clear(mediaID string) error {
	return os.RemoveAll(m.mediaDir(mediaID))
}

// ClearAll removes the entire cache directory.
func (m *Manager) ClearAll() error {
	return os.RemoveAll(m.dir)
}
