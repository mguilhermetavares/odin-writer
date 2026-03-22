package style

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

//go:embed styles/*.json
var builtinFS embed.FS

// Style defines the writing directives used to generate articles.
type Style struct {
	Name         string   `json:"name"`
	Persona      string   `json:"persona"`
	Language     string   `json:"language"`
	Tone         string   `json:"tone"`
	Structure    string   `json:"structure"`
	WordCount    string   `json:"word_count"`
	ContentRules []string `json:"content_rules"`
	StyleRules   []string `json:"style_rules"`
}

// Resolve loads a style by name (built-in) or file path (custom).
// A value is treated as a file path when it contains a path separator or ends in ".json".
func Resolve(nameOrPath string) (*Style, error) {
	if isFilePath(nameOrPath) {
		return LoadFile(nameOrPath)
	}
	return GetBuiltin(nameOrPath)
}

// GetBuiltin returns an embedded style by name.
func GetBuiltin(name string) (*Style, error) {
	data, err := builtinFS.ReadFile("styles/" + name + ".json")
	if err != nil {
		available := listBuiltin()
		return nil, fmt.Errorf("unknown style %q (built-in styles: %s)", name, strings.Join(available, ", "))
	}
	return parse(data)
}

// LoadFile loads a style from a JSON file on disk.
func LoadFile(path string) (*Style, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading style file %q: %w", path, err)
	}
	return parse(data)
}

func parse(data []byte) (*Style, error) {
	var s Style
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing style JSON: %w", err)
	}
	if s.Name == "" {
		return nil, fmt.Errorf("style JSON is missing required field: name")
	}
	return &s, nil
}

func isFilePath(s string) bool {
	return strings.Contains(s, "/") || strings.Contains(s, "\\") || strings.HasSuffix(s, ".json")
}

func listBuiltin() []string {
	entries, err := builtinFS.ReadDir("styles")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	return names
}
