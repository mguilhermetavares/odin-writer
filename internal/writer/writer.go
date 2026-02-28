package writer

import "context"

// Article is the structured output of the writing stage.
type Article struct {
	Title   string
	Excerpt string
	Body    []string
}

// Writer generates a journalistic article from a transcript.
// Implementations may use different LLMs — swap by changing the concrete type in main.go.
type Writer interface {
	GenerateArticle(ctx context.Context, transcript, mediaTitle string) (*Article, error)
}
