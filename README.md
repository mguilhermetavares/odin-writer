# odin-writer

CLI em Go que converte automaticamente episódios de podcast e vídeos do YouTube em texto escrito para websites.

A proposta não é criar conteúdo do zero, mas transformar o que já existe em formato áudio/visual em uma versão escrita e publicável — resumindo fielmente o conteúdo original de forma estruturada e automatizada.

## Como funciona

```
Source → Transcribe → Write → Publish
```

1. **Source** — identifica e baixa o áudio (YouTube via yt-dlp, ou arquivo local)
2. **Transcribe** — transcreve o áudio com Groq Whisper (segmentos paralelos para arquivos grandes)
3. **Write** — resume a transcrição em um artigo com Claude (Anthropic), seguindo o estilo configurado
4. **Publish** — cria um rascunho no Sanity CMS

Transcrições e artigos são guardados em cache por ID de mídia. Rodar duas vezes para o mesmo episódio não gera custo extra.

## Requisitos

- Go 1.25+
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) — para source=youtube (`pip install yt-dlp`)
- [ffmpeg](https://ffmpeg.org/) — para áudios > 25 MB (segmentação antes da transcrição)

## Instalação

```bash
git clone https://github.com/mguilhermetavares/odin-writer
cd odin-writer
go build -o bin/odin-writer ./cmd/odin-writer
```

## Configuração

Copie `.env.example` para `.env` e preencha:

```bash
cp .env.example .env
```

| Variável | Obrigatória | Descrição |
|----------|-------------|-----------|
| `SANITY_PROJECT_ID` | sim | ID do projeto Sanity |
| `SANITY_DATASET` | sim | Dataset (ex: `production`) |
| `SANITY_TOKEN` | sim | Token com role **Editor** |
| `ANTHROPIC_API_KEY` | sim | Chave da API Anthropic |
| `GROQ_API_KEY` | sim | Chave da API Groq |
| `YOUTUBE_CHANNEL_ID` | não | ID do canal YouTube (necessário para source=youtube e server) |
| `CLAUDE_MODEL` | não | Modelo Claude (padrão: `claude-opus-4-6`) |
| `ODIN_WRITER_HOME` | não | Diretório base para state e cache (padrão: `/var/odin-writer`) |
| `STATE_FILE` | não | Caminho do arquivo de estado (padrão: `$ODIN_WRITER_HOME/state.json`) |
| `CACHE_DIR` | não | Diretório de cache (padrão: `$ODIN_WRITER_HOME/cache`) |
| `TRANSCRIPT_LIMIT` | não | Limite de caracteres da transcrição enviada ao Claude (padrão: `150000`) |
| `POLL_INTERVAL` | não | Intervalo de polling do modo server (padrão: `24h`) |
| `STYLE` | não | Estilo de escrita (padrão: `esportivo`) |

## Uso

### Comando `run`

Processa uma fonte de mídia e publica no Sanity.

```bash
# YouTube: processa o último vídeo do canal configurado
odin-writer run

# YouTube: vídeo específico
odin-writer run -video-id VIDEO_ID

# Arquivo local
odin-writer run -source file -path ep.mp3 -title "Título do episódio"

# Sem publicar no Sanity (útil para testar)
odin-writer run -dry-run

# Forçar reprocessamento (ignora cache e estado)
odin-writer run -force

# Regenerar artigo a partir da transcrição em cache (sem publicar)
odin-writer run -rewrite-only

# Usar estilo específico
odin-writer run -style esportivo
odin-writer run -style ./meu-estilo.json
```

### Comando `server`

Polling contínuo do YouTube — verifica novos vídeos no intervalo configurado e executa a esteira completa automaticamente.

```bash
odin-writer server

# Com intervalo customizado
POLL_INTERVAL=6h odin-writer server

# Com estilo customizado
odin-writer server -style ./meu-estilo.json
```

O processo roda imediatamente ao iniciar e depois a cada `POLL_INTERVAL`. Erros são logados sem interromper o loop. Shutdown gracioso com `SIGINT` / `SIGTERM`.

### Comandos `status` e `cache`

```bash
# Ver histórico de processamento (últimos 10)
odin-writer status
odin-writer status -n 20

# Listar itens em cache
odin-writer cache list

# Limpar cache de um item específico
odin-writer cache clear -id MEDIA_ID

# Limpar todo o cache
odin-writer cache clear
```

### Formatos suportados como arquivo local

`mp3`, `mp4`, `mov`, `wav`, `webm`, `m4a` e outros formatos de áudio/vídeo aceitos pelo Groq Whisper.

## Estilos de escrita

O estilo controla o tom, idioma, estrutura e regras de conteúdo do artigo gerado. É configurado via env var `STYLE` ou flag `-style` em qualquer comando.

### Estilos built-in

| Nome | Descrição |
|------|-----------|
| `esportivo` | Jornalismo esportivo em português BR, focado em NFL/Vikings. Tom técnico e apaixonado no estilo ESPN Brasil. |

### Estilo customizado

Crie um arquivo `.json` com a seguinte estrutura e passe o caminho via `-style`:

```json
{
  "name": "meu-estilo",
  "persona": "Você é um jornalista especialista em...",
  "language": "português do Brasil",
  "tone": "técnico e acessível",
  "structure": "lide forte, desenvolvimento, conclusão",
  "word_count": "600 a 900 palavras, entre 5 e 7 parágrafos",
  "content_rules": [
    "Reflita fielmente o conteúdo da fonte",
    "Não invente informações"
  ],
  "style_rules": [
    "Use linguagem simples e direta",
    "Evite jargões sem explicação"
  ]
}
```

```bash
odin-writer run -video-id ABC123 -style ./meu-estilo.json
```

## Estrutura

```
cmd/odin-writer/main.go     # entrypoint — flags e wiring
internal/
  config/                   # carrega .env e variáveis de ambiente
  source/                   # interface Source
    youtube/                # yt-dlp wrapper (metadata + download)
    localfile/              # arquivo local
  transcriber/              # interface Transcriber
    groq/                   # Groq Whisper API (multipart, segmentos paralelos)
  writer/                   # interface Writer
    claude/                 # Anthropic SDK
  publisher/                # interface Publisher
    sanity/                 # Sanity Mutations API
  style/                    # sistema de estilos de escrita
    styles/                 # estilos built-in em JSON
  httpclient/               # HTTP client com retry (backoff exponencial)
  cache/                    # transcrição e artigo em cache por media ID
  state/                    # histórico de execuções em JSON
  pipeline/                 # Runner — orquestra os 4 estágios
  server/                   # polling loop para modo contínuo
```

## Testes

```bash
go test ./...
```
