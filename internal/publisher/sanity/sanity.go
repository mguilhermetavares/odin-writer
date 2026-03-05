package sanity

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"odin-writer/internal/writer"
)

type Publisher struct {
	projectID string
	dataset   string
	token     string
	client    *http.Client
}

func New(projectID, dataset, token string) *Publisher {
	return &Publisher{
		projectID: projectID,
		dataset:   dataset,
		token:     token,
		client:    &http.Client{},
	}
}

// CreateDraft creates a draft document in Sanity via the Mutations API.
func (p *Publisher) CreateDraft(ctx context.Context, article *writer.Article, mediaID string) (string, error) {
	docID := "drafts.odin-writer-" + mediaID

	doc := map[string]any{
		"_id":   docID,
		"_type": "article",
		"title": article.Title,
		"slug": map[string]any{
			"_type":   "slug",
			"current": slugify(article.Title),
		},
		"publishedAt": time.Now().UTC().Format(time.RFC3339),
		"excerpt":     article.Excerpt,
		"body":        paragraphsToPortableText(article.Body),
		"author":      "Minnesota Vikings BR",
		"category":    "noticias",
	}

	payload := map[string]any{
		"mutations": []map[string]any{
			{"createOrReplace": doc},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("https://%s.api.sanity.io/v2021-06-07/data/mutate/%s", p.projectID, p.dataset)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("sanity request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sanity API error %d: %s", resp.StatusCode, string(respBody))
	}

	return docID, nil
}

// portableTextBlock is a Sanity Portable Text block.
type portableTextBlock struct {
	Type     string             `json:"_type"`
	Key      string             `json:"_key"`
	Style    string             `json:"style"`
	MarkDefs []any              `json:"markDefs"`
	Children []portableTextSpan `json:"children"`
}

type portableTextSpan struct {
	Type  string `json:"_type"`
	Key   string `json:"_key"`
	Text  string `json:"text"`
	Marks []any  `json:"marks"`
}

func paragraphsToPortableText(paragraphs []string) []portableTextBlock {
	blocks := make([]portableTextBlock, 0, len(paragraphs))
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		blocks = append(blocks, portableTextBlock{
			Type:     "block",
			Key:      randHex(8),
			Style:    "normal",
			MarkDefs: []any{},
			Children: []portableTextSpan{
				{
					Type:  "span",
					Key:   randHex(8),
					Text:  p,
					Marks: []any{},
				},
			},
		})
	}
	return blocks
}

var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9\s-]`)
var multiDashRe = regexp.MustCompile(`[-\s]+`)

// slugify converts a title to a URL-friendly slug.
// Handles common Portuguese characters via manual mapping.
func slugify(title string) string {
	title = strings.ToLower(title)

	var b strings.Builder
	for _, r := range title {
		switch r {
		case 'á', 'à', 'â', 'ã', 'ä':
			b.WriteRune('a')
		case 'é', 'è', 'ê', 'ë':
			b.WriteRune('e')
		case 'í', 'ì', 'î', 'ï':
			b.WriteRune('i')
		case 'ó', 'ò', 'ô', 'õ', 'ö':
			b.WriteRune('o')
		case 'ú', 'ù', 'û', 'ü':
			b.WriteRune('u')
		case 'ç':
			b.WriteRune('c')
		case 'ñ':
			b.WriteRune('n')
		default:
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' {
				b.WriteRune(r)
			}
		}
	}

	slug := nonAlphanumRe.ReplaceAllString(b.String(), "")
	slug = multiDashRe.ReplaceAllString(slug, "-")
	return strings.Trim(slug, "-")
}

func randHex(n int) string {
	b := make([]byte, n/2+1)
	rand.Read(b)
	return fmt.Sprintf("%x", b)[:n]
}
