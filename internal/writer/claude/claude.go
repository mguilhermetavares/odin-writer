package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"odin-writer/internal/writer"
)

type Writer struct {
	client          *anthropic.Client
	model           string
	transcriptLimit int
}

func New(apiKey, model string, transcriptLimit int) *Writer {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &Writer{
		client:          &client,
		model:           model,
		transcriptLimit: transcriptLimit,
	}
}

// GenerateArticle sends the transcript to Claude and parses the JSON article response.
func (w *Writer) GenerateArticle(ctx context.Context, transcript, mediaTitle string) (*writer.Article, error) {
	excerpt := transcript
	if len(excerpt) > w.transcriptLimit {
		log.Printf("  warning: transcript truncated from %d to %d chars", len(excerpt), w.transcriptLimit)
		excerpt = excerpt[:w.transcriptLimit]
	}

	prompt := w.buildPrompt(excerpt, mediaTitle)

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

	return parseArticleJSON(text)
}

func (w *Writer) buildPrompt(transcript, videoTitle string) string {
	return fmt.Sprintf(`Você é um redator esportivo especialista em NFL e Minnesota Vikings, escrevendo para o Minnesota Vikings BR, o maior fansite brasileiro do time.

Com base na transcrição abaixo de um episódio do podcast "Minnesota Vikings BR", escreva um artigo em português do Brasil que reproduza fielmente o conteúdo discutido.

Título original do episódio: %s

Transcrição:
%s

---

Retorne SOMENTE um JSON válido com esta estrutura (sem markdown, sem `+"`"+`json`+"`"+`):
{
  "title": "título criativo e jornalístico (não repita o título do episódio)",
  "excerpt": "resumo em 2-3 frases apresentando o tema central do artigo",
  "body": [
    "parágrafo 1",
    "parágrafo 2",
    "..."
  ]
}

Diretrizes de conteúdo:
- O artigo deve refletir fielmente o que foi discutido no podcast — as opiniões e análises são dos apresentadores, não suas
- Não invente argumentos, posições ou informações que não estejam na transcrição
- Não mencione o podcast, os apresentadores ou o programa dentro do texto — escreva como artigo independente
- Estrutura: lide forte (o quê e por que importa), desenvolvimento dos temas principais, conclusão
- Tamanho: 800 a 1.200 palavras, entre 7 e 9 parágrafos
- Cada parágrafo deve desenvolver uma ideia de forma fluida, sem enumerar tópicos em sequência

Diretrizes de estilo:
- Tom: técnico, direto e apaixonado, no estilo do The Playoffs e da ESPN Brasil
- Termos técnicos da NFL em inglês sem tradução: QB, WR, RB, TE, OL, DL, LB, CB, blitz, sack, snap, draft, touchdown, field goal, red zone, first down, playoff, wildcard, bye week
- Jardas podem ser usadas normalmente em português
- Proibido usar travessão (—) em qualquer parte do texto
- Sem bullet points ou listas no corpo do artigo
- Linguagem para fãs experientes: não explique conceitos básicos`, videoTitle, transcript)
}

type articleJSON struct {
	Title   string   `json:"title"`
	Excerpt string   `json:"excerpt"`
	Body    []string `json:"body"`
}

func parseArticleJSON(text string) (*writer.Article, error) {
	// Extract JSON even if surrounded by other text
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON object found in claude response")
	}

	var a articleJSON
	if err := json.Unmarshal([]byte(text[start:end+1]), &a); err != nil {
		return nil, fmt.Errorf("parsing article JSON: %w", err)
	}

	if a.Title == "" || a.Excerpt == "" || len(a.Body) == 0 {
		return nil, fmt.Errorf("incomplete article: missing title, excerpt or body")
	}

	return &writer.Article{
		Title:   a.Title,
		Excerpt: a.Excerpt,
		Body:    a.Body,
	}, nil
}
