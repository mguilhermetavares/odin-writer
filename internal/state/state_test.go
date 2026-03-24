package state

import (
	"path/filepath"
	"testing"
	"time"
)

func makeEntry(mediaID, title string) Entry {
	return Entry{
		SourceID:     "youtube",
		MediaID:      mediaID,
		ProcessedAt:  time.Now(),
		ArticleTitle: title,
	}
}

func TestRecord_SavesFirstEntry(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "state.json"))

	entry := makeEntry("vid1", "First Article")
	if err := m.Record(entry); err != nil {
		t.Fatalf("Record: %v", err)
	}

	recent, err := m.Recent(10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("Recent returned %d entries; want 1", len(recent))
	}
	if recent[0].MediaID != "vid1" {
		t.Errorf("MediaID = %q; want %q", recent[0].MediaID, "vid1")
	}
}

func TestWasProcessed_ReturnsFalseIfNeverProcessed(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "state.json"))

	ok, err := m.WasProcessed("unknown")
	if err != nil {
		t.Fatalf("WasProcessed: %v", err)
	}
	if ok {
		t.Error("WasProcessed = true; want false for unknown ID")
	}
}

func TestWasProcessed_ReturnsTrueAfterRecord(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "state.json"))

	if err := m.Record(makeEntry("vid1", "Some Title")); err != nil {
		t.Fatalf("Record: %v", err)
	}

	ok, err := m.WasProcessed("vid1")
	if err != nil {
		t.Fatalf("WasProcessed: %v", err)
	}
	if !ok {
		t.Error("WasProcessed = false; want true after Record")
	}
}

func TestRecent_ReturnsLastNEntriesInReverseOrder(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "state.json"))

	ids := []string{"vid1", "vid2", "vid3", "vid4", "vid5"}
	for _, id := range ids {
		if err := m.Record(makeEntry(id, id+" title")); err != nil {
			t.Fatalf("Record(%s): %v", id, err)
		}
	}

	recent, err := m.Recent(3)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(recent) != 3 {
		t.Fatalf("Recent(3) returned %d entries; want 3", len(recent))
	}
	// Most recent first: vid5, vid4, vid3
	wantOrder := []string{"vid5", "vid4", "vid3"}
	for i, want := range wantOrder {
		if recent[i].MediaID != want {
			t.Errorf("recent[%d].MediaID = %q; want %q", i, recent[i].MediaID, want)
		}
	}
}

func TestRecent_ReturnsAllIfNLargerThanTotal(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "state.json"))

	for _, id := range []string{"vid1", "vid2"} {
		if err := m.Record(makeEntry(id, id)); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	recent, err := m.Recent(100)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(recent) != 2 {
		t.Errorf("Recent(100) returned %d entries; want 2", len(recent))
	}
}

func TestLastEntry_ReturnsNilIfEmpty(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "state.json"))

	entry, err := m.LastEntry()
	if err != nil {
		t.Fatalf("LastEntry: %v", err)
	}
	if entry != nil {
		t.Errorf("LastEntry = %v; want nil", entry)
	}
}

func TestLastEntry_ReturnsMostRecentEntry(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "state.json"))

	for _, id := range []string{"vid1", "vid2", "vid3"} {
		if err := m.Record(makeEntry(id, id)); err != nil {
			t.Fatalf("Record(%s): %v", id, err)
		}
	}

	last, err := m.LastEntry()
	if err != nil {
		t.Fatalf("LastEntry: %v", err)
	}
	if last == nil {
		t.Fatal("LastEntry = nil; want non-nil")
	}
	if last.MediaID != "vid3" {
		t.Errorf("LastEntry.MediaID = %q; want %q", last.MediaID, "vid3")
	}
}

func TestRecord_DuplicateIDUpdatesEntryWithoutDuplicating(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "state.json"))

	if err := m.Record(makeEntry("vid1", "Original Title")); err != nil {
		t.Fatalf("Record (first): %v", err)
	}

	updated := makeEntry("vid1", "Updated Title")
	if err := m.Record(updated); err != nil {
		t.Fatalf("Record (second): %v", err)
	}

	recent, err := m.Recent(100)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("Recent returned %d entries; want 1 (no duplicates)", len(recent))
	}
	if recent[0].ArticleTitle != "Updated Title" {
		t.Errorf("ArticleTitle = %q; want %q", recent[0].ArticleTitle, "Updated Title")
	}
}

func TestLoad_NonExistentFileReturnsEmptyStateWithoutError(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "does-not-exist", "state.json"))

	// load is unexported; exercise it through the public API
	recent, err := m.Recent(10)
	if err != nil {
		t.Fatalf("Recent on missing file returned error: %v", err)
	}
	if len(recent) != 0 {
		t.Errorf("Recent on missing file returned %d entries; want 0", len(recent))
	}
}

func TestPersistence_NewManagerReadsStateSavedByPreviousManager(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	m1 := New(statePath)
	if err := m1.Record(makeEntry("vid1", "Article One")); err != nil {
		t.Fatalf("Record (m1): %v", err)
	}
	if err := m1.Record(makeEntry("vid2", "Article Two")); err != nil {
		t.Fatalf("Record (m1 vid2): %v", err)
	}

	// Create a brand-new Manager pointing to the same file
	m2 := New(statePath)

	ok, err := m2.WasProcessed("vid1")
	if err != nil {
		t.Fatalf("WasProcessed (m2): %v", err)
	}
	if !ok {
		t.Error("WasProcessed(vid1) via m2 = false; want true")
	}

	last, err := m2.LastEntry()
	if err != nil {
		t.Fatalf("LastEntry (m2): %v", err)
	}
	if last == nil || last.MediaID != "vid2" {
		t.Errorf("LastEntry(m2).MediaID = %v; want vid2", last)
	}
}
