package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry records a single processed media item.
type Entry struct {
	SourceID     string    `json:"source"`
	MediaID      string    `json:"media_id"`
	ProcessedAt  time.Time `json:"processed_at"`
	ArticleTitle string    `json:"article_title"`
}

type state struct {
	Entries []Entry `json:"entries"`
}

// Manager persists the processing history to a JSON file.
type Manager struct {
	path string
}

func New(path string) *Manager {
	return &Manager{path: path}
}

// LastEntry returns the most recently processed entry, or nil if none.
func (m *Manager) LastEntry() (*Entry, error) {
	s, err := m.load()
	if err != nil {
		return nil, err
	}
	if len(s.Entries) == 0 {
		return nil, nil
	}
	e := s.Entries[len(s.Entries)-1]
	return &e, nil
}

// WasProcessed returns true if the given mediaID was already processed.
func (m *Manager) WasProcessed(mediaID string) (bool, error) {
	s, err := m.load()
	if err != nil {
		return false, err
	}
	for _, e := range s.Entries {
		if e.MediaID == mediaID {
			return true, nil
		}
	}
	return false, nil
}

// Record appends a new entry to the state file.
func (m *Manager) Record(entry Entry) error {
	s, err := m.load()
	if err != nil {
		return err
	}

	// Update existing entry if mediaID already present
	for i, e := range s.Entries {
		if e.MediaID == entry.MediaID {
			s.Entries[i] = entry
			return m.save(s)
		}
	}

	s.Entries = append(s.Entries, entry)
	return m.save(s)
}

func (m *Manager) load() (*state, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &state{}, nil
		}
		return nil, fmt.Errorf("reading state file: %w", err)
	}

	var s state
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}
	return &s, nil
}

func (m *Manager) save(s *state) error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.path, data, 0o644)
}
