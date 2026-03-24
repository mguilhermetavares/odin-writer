package style

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGetBuiltinEsportivoLoadsSuccessfully checks that the "esportivo" built-in
// style can be loaded without error.
func TestGetBuiltinEsportivoLoadsSuccessfully(t *testing.T) {
	s, err := GetBuiltin("esportivo")
	if err != nil {
		t.Fatalf("GetBuiltin(%q) error = %v", "esportivo", err)
	}
	if s == nil {
		t.Fatal("GetBuiltin returned nil style without error")
	}
}

// TestGetBuiltinUnknownNameReturnsError verifies that requesting a non-existent
// built-in style name returns an error.
func TestGetBuiltinUnknownNameReturnsError(t *testing.T) {
	_, err := GetBuiltin("nonexistent-style-xyz")
	if err == nil {
		t.Error("expected error for unknown built-in style, got nil")
	}
}

// TestLoadFileValidJSON checks that LoadFile correctly parses a valid JSON style
// file written to a temporary directory.
func TestLoadFileValidJSON(t *testing.T) {
	content := `{
		"name": "test-style",
		"persona": "A test persona",
		"language": "english",
		"tone": "neutral",
		"structure": "intro, body, conclusion",
		"word_count": "500",
		"content_rules": ["rule one"],
		"style_rules": ["style rule one"]
	}`

	path := filepath.Join(t.TempDir(), "test-style.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writing temp style file: %v", err)
	}

	s, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile error = %v", err)
	}
	if s.Name != "test-style" {
		t.Errorf("Name = %q, want %q", s.Name, "test-style")
	}
}

// TestLoadFileNonexistentPathReturnsError verifies that LoadFile returns an
// error when the file does not exist.
func TestLoadFileNonexistentPathReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")

	_, err := LoadFile(path)
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}

// TestLoadFileMalformedJSONReturnsError checks that LoadFile returns an error
// when the file contains invalid JSON.
func TestLoadFileMalformedJSONReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0600); err != nil {
		t.Fatalf("writing malformed JSON file: %v", err)
	}

	_, err := LoadFile(path)
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

// TestResolveWithPlainNameUsesGetBuiltin checks that Resolve treats a plain
// name (no slash, no .json suffix) as a built-in style lookup.
func TestResolveWithPlainNameUsesGetBuiltin(t *testing.T) {
	s, err := Resolve("esportivo")
	if err != nil {
		t.Fatalf("Resolve(%q) error = %v", "esportivo", err)
	}
	if s == nil {
		t.Fatal("Resolve returned nil style without error")
	}
}

// TestResolveWithSlashInPathUsesLoadFile verifies that Resolve treats a value
// containing "/" as a file path, delegating to LoadFile.
func TestResolveWithSlashInPathUsesLoadFile(t *testing.T) {
	content := `{"name":"file-style","persona":"p","language":"en","tone":"t","structure":"s","word_count":"100","content_rules":[],"style_rules":[]}`

	dir := t.TempDir()
	path := filepath.Join(dir, "file-style.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writing style file: %v", err)
	}

	// path contains "/" so Resolve should call LoadFile.
	s, err := Resolve(path)
	if err != nil {
		t.Fatalf("Resolve(%q) error = %v", path, err)
	}
	if s.Name != "file-style" {
		t.Errorf("Name = %q, want %q", s.Name, "file-style")
	}
}

// TestResolveWithDotJSONSuffixUsesLoadFile verifies that Resolve treats a value
// ending in ".json" as a file path, even if it has no directory component.
func TestResolveWithDotJSONSuffixUsesLoadFile(t *testing.T) {
	// A name that ends in ".json" should be treated as a file path.
	// The file won't exist, so we expect an error from LoadFile — not from
	// GetBuiltin (which would give "unknown style" instead).
	_, err := Resolve("some-style.json")
	if err == nil {
		t.Fatal("expected error for nonexistent .json file, got nil")
	}
	// The error should mention the file path, not "unknown style".
	if strings.Contains(err.Error(), "unknown style") {
		t.Errorf("error %q looks like GetBuiltin error; expected LoadFile error", err.Error())
	}
}

// TestEsportivoStyleHasRequiredFields verifies that the "esportivo" built-in
// style has non-empty Name and Persona fields after loading.
func TestEsportivoStyleHasRequiredFields(t *testing.T) {
	s, err := GetBuiltin("esportivo")
	if err != nil {
		t.Fatalf("GetBuiltin(%q) error = %v", "esportivo", err)
	}
	if s.Name == "" {
		t.Error("esportivo style has empty Name")
	}
	if s.Persona == "" {
		t.Error("esportivo style has empty Persona")
	}
}

// TestIsFilePathDetectsSlash checks the internal isFilePath logic via Resolve:
// a name with "/" is routed to LoadFile (file-not-found error expected).
func TestIsFilePathDetectsSlash(t *testing.T) {
	_, err := Resolve("/tmp/nonexistent-style.json")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), "unknown style") {
		t.Errorf("path with '/' should be treated as file, not built-in; got: %v", err)
	}
}

// TestLoadFileMissingNameFieldReturnsError verifies that LoadFile returns an
// error when the JSON is valid but the required "name" field is absent.
func TestLoadFileMissingNameFieldReturnsError(t *testing.T) {
	content := `{"persona":"p","language":"en","tone":"t","structure":"s","word_count":"100","content_rules":[],"style_rules":[]}`

	path := filepath.Join(t.TempDir(), "no-name.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writing style file: %v", err)
	}

	_, err := LoadFile(path)
	if err == nil {
		t.Error("expected error for style JSON missing 'name' field, got nil")
	}
}

// TestGetBuiltinUnknownNameErrorMentionsAvailable checks that the error message
// for an unknown built-in style lists the available style names.
func TestGetBuiltinUnknownNameErrorMentionsAvailable(t *testing.T) {
	_, err := GetBuiltin("does-not-exist")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "esportivo") {
		t.Errorf("error message %q should mention available styles (e.g. 'esportivo')", err.Error())
	}
}
