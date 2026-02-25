package localfile

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"odin-writer/internal/source"
)

// Source wraps a local audio file (mp3, mp4, mov, wav, webm, etc.).
// No download is performed — AudioPath points directly to the provided file.
type Source struct{}

func New() *Source {
	return &Source{}
}

// Prepare validates and returns the local file as a Media.
// opts.Path is required. opts.Title is optional (defaults to filename without extension).
func (s *Source) Prepare(_ context.Context, opts source.Options, _ string) (*source.Media, error) {
	if opts.Path == "" {
		return nil, fmt.Errorf("--path is required for source=file")
	}

	info, err := os.Stat(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", opts.Path)
	}
	_ = info

	id, err := fileID(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("computing file ID: %w", err)
	}

	title := opts.Title
	if title == "" {
		base := filepath.Base(opts.Path)
		title = strings.TrimSuffix(base, filepath.Ext(base))
	}

	return &source.Media{
		ID:        id,
		Title:     title,
		AudioPath: opts.Path,
		SourceID:  "localfile",
	}, nil
}

// fileID returns a short hash of the file path + size to use as a stable ID.
func fileID(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	// Hash first 64KB + file size for a fast, stable ID
	_, err = io.CopyN(h, f, 64*1024)
	if err != nil && err != io.EOF {
		return "", err
	}
	fmt.Fprintf(h, ":%d", info.Size())

	return fmt.Sprintf("%x", h.Sum(nil))[:16], nil
}
