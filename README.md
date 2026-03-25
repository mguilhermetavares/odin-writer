# odin-writer

<p align="center">
  <img src="assets/mascot.png" alt="odin-writer mascot" width="160" />
</p>

<p align="center">
  <a href="https://github.com/mguilhermetavares/odin-writer/actions/workflows/ci.yml"><img src="https://github.com/mguilhermetavares/odin-writer/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/mguilhermetavares/odin-writer/releases"><img src="https://img.shields.io/github/v/release/mguilhermetavares/odin-writer" alt="Release"></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/go-1.25+-00ADD8?logo=go&logoColor=white" alt="Go"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-green" alt="License"></a>
</p>

A Go CLI that automatically converts podcast episodes and YouTube videos into written articles for websites.

The goal is not to create content from scratch, but to transform what already exists in audio/video format into a structured, publishable written version — faithfully summarizing the original content.

## How it works

```
Source → Transcribe → Write → Publish
```

1. **Source** — identifies and downloads the audio (YouTube via yt-dlp, or a local file)
2. **Transcribe** — transcribes audio with Groq Whisper; large files are split into segments automatically
3. **Write** — summarizes the transcript into an article using Claude (Anthropic), following a configured writing style
4. **Publish** — creates a draft in Sanity CMS

Transcripts and articles are cached per media ID. Running the same episode twice has no extra cost.

## Prerequisites

| Dependency | Required | Install |
|------------|----------|---------|
| [yt-dlp](https://github.com/yt-dlp/yt-dlp) | For `source=youtube` | `brew install yt-dlp` or `pip install yt-dlp` |
| [ffmpeg](https://ffmpeg.org/) | For audio files > 25 MB | `brew install ffmpeg` |

If `yt-dlp` is not installed and you run `odin-writer run` with the default YouTube source, you will get a clear error message with installation instructions.

## Installation

### Homebrew (macOS / Linux)

```bash
brew tap mguilhermetavares/tap
brew install odin-writer
```

> Homebrew automatically installs `yt-dlp` as a dependency.

### Build from source

Requires Go 1.25+.

```bash
git clone https://github.com/mguilhermetavares/odin-writer
cd odin-writer
go build -o bin/odin-writer ./cmd/odin-writer
```

## Configuration

Copy `.env.example` to `.env` and fill in your credentials:

```bash
cp .env.example .env
```

| Variable | Required | Description |
|----------|----------|-------------|
| `SANITY_PROJECT_ID` | yes | Sanity project ID |
| `SANITY_DATASET` | yes | Dataset name (e.g. `production`) |
| `SANITY_TOKEN` | yes | Token with **Editor** role |
| `ANTHROPIC_API_KEY` | yes | Anthropic API key |
| `GROQ_API_KEY` | yes | Groq API key |
| `YOUTUBE_CHANNEL_ID` | no | YouTube channel ID (required for `source=youtube` and `server` mode) |
| `CLAUDE_MODEL` | no | Claude model to use (default: `claude-opus-4-6`) |
| `ODIN_WRITER_HOME` | no | Base directory for state and cache (default: `/var/odin-writer`) |
| `TRANSCRIPT_LIMIT` | no | Max characters of transcript sent to Claude (default: `150000`) |
| `POLL_INTERVAL` | no | Polling interval for server mode (default: `24h`) |
| `STYLE` | no | Writing style name or path (default: `esportivo`) |

## Usage

### `run`

Process a media source and publish to Sanity.

```bash
# YouTube: process the latest video from the configured channel
odin-writer run

# YouTube: specific video
odin-writer run -video-id VIDEO_ID

# Local file
odin-writer run -source file -path episode.mp3 -title "Episode title"

# Skip publishing (useful for testing)
odin-writer run -dry-run

# Force reprocessing (ignore cache and state)
odin-writer run -force

# Regenerate article from cached transcript without publishing
odin-writer run -rewrite-only

# Use a specific writing style
odin-writer run -style esportivo
odin-writer run -style ./my-style.json

# Run detached in the background (logs written to ODIN_WRITER_HOME/logs/)
odin-writer run -background
```

> **Tip:** videos longer than 1 hour will show a warning suggesting `-background`.

### `server`

Continuous YouTube polling — checks for new videos at the configured interval and runs the full pipeline automatically.

```bash
odin-writer server

# Custom interval
POLL_INTERVAL=6h odin-writer server

# Custom style
odin-writer server -style ./my-style.json
```

The process runs immediately on start, then again every `POLL_INTERVAL`. Errors are logged without stopping the loop. Graceful shutdown on `SIGINT` / `SIGTERM`.

### `status` and `cache`

```bash
# Show processing history (last 10 entries)
odin-writer status
odin-writer status -n 20

# List cached items
odin-writer cache list

# Clear cache for a specific item
odin-writer cache clear -id MEDIA_ID

# Clear all cache
odin-writer cache clear
```

### `version`

```bash
odin-writer version
odin-writer --version
```

### Supported local file formats

`mp3`, `mp4`, `mov`, `wav`, `webm`, `m4a` and other audio/video formats accepted by Groq Whisper.

## Writing styles

Styles control the tone, language, structure, and content rules of the generated article. Set via the `STYLE` env var or the `-style` flag on any command.

### Built-in styles

| Name | Description |
|------|-------------|
| `esportivo` | Sports journalism in Brazilian Portuguese, focused on NFL/Vikings. Technical and passionate tone in the ESPN Brasil style. |

### Custom style

Create a `.json` file with the following structure and pass its path via `-style`:

```json
{
  "name": "my-style",
  "persona": "You are a technology journalist...",
  "language": "English",
  "tone": "technical and accessible",
  "structure": "strong lede, development, conclusion",
  "word_count": "600 to 900 words, 5 to 7 paragraphs",
  "content_rules": [
    "Faithfully reflect the source content",
    "Do not invent information"
  ],
  "style_rules": [
    "Use plain and direct language",
    "Avoid unexplained jargon"
  ]
}
```

```bash
odin-writer run -video-id ABC123 -style ./my-style.json
```

## Project structure

```
cmd/odin-writer/main.go     # entrypoint — flag parsing and wiring
internal/
  config/                   # loads .env and environment variables
  source/                   # Source interface
    youtube/                # yt-dlp wrapper (metadata + download)
    localfile/              # local file source
  transcriber/              # Transcriber interface
    groq/                   # Groq Whisper API (multipart, parallel segments, rate limiter)
  writer/                   # Writer interface
    claude/                 # Anthropic SDK
  publisher/                # Publisher interface
    sanity/                 # Sanity Mutations API
  style/                    # writing style system
    styles/                 # built-in styles as embedded JSON
  httpclient/               # HTTP client with retry (exponential backoff + jitter)
  cache/                    # transcript and article cache per media ID
  state/                    # execution history as JSON
  pipeline/                 # Runner — orchestrates the 4 stages
  server/                   # polling loop for continuous mode
```

## Testing

```bash
go test ./...
```

Tests use `testing/synctest` for time-dependent scenarios (rate limiter, retry backoff, server polling) and mock HTTP servers for all three external APIs (Groq, Anthropic, Sanity).

## License

MIT
