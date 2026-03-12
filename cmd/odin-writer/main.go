package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"odin-writer/internal/cache"
	"odin-writer/internal/config"
	"odin-writer/internal/pipeline"
	"odin-writer/internal/publisher/sanity"
	"odin-writer/internal/source"
	"odin-writer/internal/source/localfile"
	"odin-writer/internal/source/youtube"
	"odin-writer/internal/state"
	"odin-writer/internal/transcriber/groq"
	"odin-writer/internal/writer/claude"
)

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: odin-writer <run> [flags]")
		os.Exit(1)
	}

	envFile := os.Getenv("ODIN_WRITER_ENV")
	if envFile == "" {
		envFile = ".env"
	}

	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:], envFile)
	case "-h", "--help", "help":
		fmt.Println("usage: odin-writer run [flags]")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runCmd(args []string, envFile string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	srcType := fs.String("source", "youtube", "source type: youtube, file")
	videoID := fs.String("video-id", "", "YouTube video ID")
	path := fs.String("path", "", "local file path (for -source=file)")
	title := fs.String("title", "", "media title override")
	force := fs.Bool("force", false, "ignore state and cache")
	dryRun := fs.Bool("dry-run", false, "run without publishing to Sanity")
	rewriteOnly := fs.Bool("rewrite-only", false, "regenerate article from cached transcript")
	fs.Parse(args)

	cfg := mustLoadConfig(envFile)

	cacheManager := cache.New(cfg.CacheDir)
	stateManager := state.New(cfg.StateFile)
	transcriber := groq.New(cfg.GroqAPIKey)
	articleWriter := claude.New(cfg.AnthropicAPIKey, cfg.ClaudeModel, cfg.TranscriptLimit)
	pub := sanity.New(cfg.SanityProjectID, cfg.SanityDataset, cfg.SanityToken)

	src := buildSource(cfg, *srcType)
	runner := pipeline.NewRunner(src, transcriber, articleWriter, pub, cacheManager, stateManager)

	opts := pipeline.RunOptions{
		Source:      *srcType,
		VideoID:     *videoID,
		Path:        *path,
		Title:       *title,
		Force:       *force,
		DryRun:      *dryRun,
		RewriteOnly: *rewriteOnly,
	}

	if err := runner.Run(context.Background(), opts); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func buildSource(cfg *config.Config, srcType string) source.Source {
	switch srcType {
	case "youtube":
		return youtube.New(cfg.YouTubeChannelID)
	case "file":
		return localfile.New()
	default:
		log.Fatalf("unknown source: %s (valid: youtube, file)", srcType)
		return nil
	}
}

func mustLoadConfig(envFile string) *config.Config {
	cfg, err := config.Load(envFile)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	return cfg
}
