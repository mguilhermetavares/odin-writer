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
	prompt := w.buildPrompt(transcript, mediaTitle)

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

func (w *Writer) buildPrompt(transcript, videoTitle string) string {
	return fmt.Sprintf(`Você é um redator esportivo especialista em NFL e Minnesota Vikings, escrevendo para o Minnesota Vikings BR, o maior fansite brasileiro do time.

Com base na transcrição abaixo de um episódio do podcast "Minnesota Vikings BR", escreva um artigo em português do Brasil que reproduza fielmente o conteúdo discutido.

Título original do episódio: %s

Transcrição:
%s

---

Escreva o artigo em texto corrido. Comece pelo título na primeira linha, seguido pelos parágrafos do corpo.

Diretrizes de conteúdo:
- O artigo deve refletir fielmente o que foi discutido no podcast
- Não invente argumentos ou informações que não estejam na transcrição
- Estrutura: lide forte, desenvolvimento dos temas principais, conclusão
- Tamanho: 800 a 1.200 palavras, entre 7 e 9 parágrafos

Diretrizes de estilo:
- Tom: técnico, direto e apaixonado
- Termos técnicos da NFL em inglês: QB, WR, RB, TE, blitz, sack, draft, touchdown
- Proibido usar travessão (—) em qualquer parte do texto`, videoTitle, transcript)
}
