package publisher

import (
	"context"

	"odin-writer/internal/writer"
)

// Publisher creates a draft article in a CMS.
// Returns the document ID of the created draft.
type Publisher interface {
	CreateDraft(ctx context.Context, article *writer.Article, mediaID string) (string, error)
}
