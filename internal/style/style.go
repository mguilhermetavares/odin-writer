package style

import (
	"fmt"
	"strings"
)

// Style defines the writing directives used to generate articles.
type Style struct {
	// Name is the style identifier (e.g. "esportivo").
	Name string

	// Persona is the opening system instruction describing who the writer is.
	Persona string

	// Language is the output language (e.g. "português do Brasil").
	Language string

	// Tone describes the desired tone (e.g. "técnico, direto e apaixonado").
	Tone string

	// Structure describes how the article should be organized.
	Structure string

	// WordCount specifies length and paragraph count guidance.
	WordCount string

	// ContentRules are directives about content faithfulness and accuracy.
	ContentRules []string

	// StyleRules are directives about language, vocabulary, and formatting.
	StyleRules []string
}

var registry = map[string]*Style{
	"esportivo": Esportivo,
}

// Get returns a registered style by name.
func Get(name string) (*Style, error) {
	s, ok := registry[name]
	if !ok {
		names := make([]string, 0, len(registry))
		for k := range registry {
			names = append(names, k)
		}
		return nil, fmt.Errorf("unknown style %q (available: %s)", name, strings.Join(names, ", "))
	}
	return s, nil
}

// Register adds a custom style to the registry.
// Panics if name is already taken by a built-in style.
func Register(s *Style) {
	if _, exists := registry[s.Name]; exists {
		panic(fmt.Sprintf("style %q is already registered", s.Name))
	}
	registry[s.Name] = s
}
