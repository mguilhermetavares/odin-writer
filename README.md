# odin-writer

CLI em Go que gera artigos jornalísticos a partir de episódios de podcast ou vídeos do YouTube.

## Como funciona

```
Source → Transcribe → Write → Publish
```

1. **Source** — identifica e baixa o áudio (YouTube via yt-dlp, ou arquivo local)
2. **Transcribe** — transcreve o áudio com Groq Whisper
3. **Write** — gera o artigo com Claude (Anthropic)
4. **Publish** — cria um rascunho no Sanity CMS

Transcrições e artigos são guardados em cache por ID de mídia. Rodar duas vezes para o mesmo episódio não gera custo extra.

## Requisitos

- Go 1.25+
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) — para source=youtube (`pip install yt-dlp`)
- [ffmpeg](https://ffmpeg.org/) — para áudios > 25 MB (segmentação antes da transcrição)

## Instalação

```bash
git clone <repo>
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
| `YOUTUBE_CHANNEL_ID` | não | ID do canal YouTube (necessário para source=youtube) |
| `CLAUDE_MODEL` | não | Modelo Claude (padrão: `claude-opus-4-6`) |
| `ODIN_WRITER_HOME` | não | Diretório base para state e cache (padrão: `/var/odin-writer`) |
| `STATE_FILE` | não | Caminho do arquivo de estado (padrão: `$ODIN_WRITER_HOME/state.json`) |
| `CACHE_DIR` | não | Diretório de cache (padrão: `$ODIN_WRITER_HOME/cache`) |

## Uso

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

# Ver o último item processado
odin-writer status

# Listar itens em cache
odin-writer cache list

# Limpar cache de um item específico
odin-writer cache clear -id MEDIA_ID
```

### Formatos suportados como arquivo local

`mp3`, `mp4`, `mov`, `wav`, `webm`, `m4a` e outros formatos de áudio/vídeo aceitos pelo Groq Whisper.

## Estrutura

```
cmd/odin-writer/main.go     # entrypoint — flags e wiring
internal/
  config/                   # carrega .env e variáveis de ambiente
  source/                   # interface Source
    youtube/                # yt-dlp wrapper
    localfile/              # arquivo local
  transcriber/              # interface Transcriber
    groq/                   # Groq Whisper API
  writer/                   # interface Writer
    claude/                 # Anthropic SDK
  publisher/                # interface Publisher
    sanity/                 # Sanity Mutations API
  cache/                    # transcrição e artigo em cache por media ID
  state/                    # histórico de execuções em JSON
  pipeline/                 # Runner — orquestra os 4 estágios
```

## Testes

```bash
go test ./...
```
