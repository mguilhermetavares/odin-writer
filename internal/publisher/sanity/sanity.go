package sanity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
		"_id":         docID,
		"_type":       "article",
		"title":       article.Title,
		"publishedAt": time.Now().UTC().Format(time.RFC3339),
		"excerpt":     article.Excerpt,
		"body":        strings.Join(article.Body, "\n\n"),
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
