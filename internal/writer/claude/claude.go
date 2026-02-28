package claude

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"odin-writer/internal/writer"
)

type Writer struct {
	client *anthropic.Client
	model  string
}

func New(apiKey, model string, _ int) *Writer {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &Writer{
		client: &client,
		model:  model,
	}
}

// GenerateArticle sends the transcript to Claude and returns an article.
func (w *Writer) GenerateArticle(ctx context.Context, transcript, mediaTitle string) (*writer.Article, error) {
	prompt := fmt.Sprintf("Você é um jornalista esportivo especialista em NFL e Minnesota Vikings. Com base na transcrição abaixo, escreva um artigo em português do Brasil.\n\nTítulo: %s\n\nTranscrição:\n%s", mediaTitle, transcript)

	resp, err := w.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(w.model),
		MaxTokens: 4096,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("claude API: %w", err)
	}

	var text string
	for _, block := range resp.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			text = v.Text
		}
	}

	if text == "" {
		return nil, fmt.Errorf("claude returned empty response")
	}

	var body []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			body = append(body, line)
		}
	}

	title := mediaTitle
	if len(body) > 0 {
		title = body[0]
		body = body[1:]
	}

	return &writer.Article{
		Title:   title,
		Excerpt: "",
		Body:    body,
	}, nil
}
