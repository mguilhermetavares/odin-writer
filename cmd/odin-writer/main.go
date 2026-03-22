package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"odin-writer/internal/cache"
	"odin-writer/internal/config"
	"odin-writer/internal/pipeline"
	"odin-writer/internal/publisher/sanity"
	"odin-writer/internal/server"
	"odin-writer/internal/source"
	"odin-writer/internal/source/localfile"
	"odin-writer/internal/source/youtube"
	"odin-writer/internal/state"
	"odin-writer/internal/style"
	"odin-writer/internal/transcriber/groq"
	"odin-writer/internal/writer/claude"
)

const usage = `usage: odin-writer <command> [flags]

Commands:
  run          Process a media source and publish to Sanity
  server       Poll YouTube for new videos and run the pipeline automatically
  status       Show recent processing history
  cache        Manage cached transcripts and articles

Run "odin-writer <command> -h" for command-specific help.
`

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	envFile := os.Getenv("ODIN_WRITER_ENV")
	if envFile == "" {
		envFile = ".env"
	}

	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:], envFile)
	case "server":
		serverCmd(os.Args[2:], envFile)
	case "status":
		statusCmd(os.Args[2:], envFile)
	case "cache":
		cacheCmd(os.Args[2:], envFile)
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
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
	styleFlag := fs.String("style", "", "style name or path to a JSON style file (overrides STYLE env var)")
	fs.Parse(args)

	cfg := mustLoadConfig(envFile)
	if *styleFlag != "" {
		cfg.StyleName = *styleFlag
	}
	src := buildSource(cfg, *srcType)
	runner := mustBuildRunner(cfg, src)

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

func serverCmd(args []string, envFile string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	styleFlag := fs.String("style", "", "style name or path to a JSON style file (overrides STYLE env var)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: odin-writer server [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Polls YouTube for new videos and runs the full pipeline automatically.")
		fmt.Fprintln(os.Stderr, "Interval is controlled by POLL_INTERVAL env var (default: 24h).")
		fmt.Fprintln(os.Stderr, "")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	cfg := mustLoadConfig(envFile)
	if *styleFlag != "" {
		cfg.StyleName = *styleFlag
	}
	if cfg.YouTubeChannelID == "" {
		log.Fatal("server mode requires YOUTUBE_CHANNEL_ID")
	}

	src := youtube.New(cfg.YouTubeChannelID)
	runner := mustBuildRunner(cfg, src)
	srv := server.New(runner, cfg.PollInterval)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srv.Run(ctx)
}

func statusCmd(args []string, envFile string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	n := fs.Int("n", 10, "number of recent entries to show")
	fs.Parse(args)

	cfg := mustLoadConfig(envFile)
	stateManager := state.New(cfg.StateFile)

	entries, err := stateManager.Recent(*n)
	if err != nil {
		log.Fatalf("error reading state: %v", err)
	}

	if len(entries) == 0 {
		fmt.Println("no processed articles yet")
		return
	}

	fmt.Printf("%-12s  %-26s  %s\n", "media ID", "processed at", "title")
	fmt.Println("------------  --------------------------  -----")
	for _, e := range entries {
		fmt.Printf("%-12s  %-26s  %s\n",
			e.MediaID,
			e.ProcessedAt.Format("2006-01-02 15:04:05 MST"),
			e.ArticleTitle,
		)
	}
}

func cacheCmd(args []string, envFile string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: odin-writer cache <list|clear> [flags]")
		os.Exit(1)
	}

	cfg := mustLoadConfig(envFile)
	cacheManager := cache.New(cfg.CacheDir)

	switch args[0] {
	case "list":
		entries, err := cacheManager.List()
		if err != nil {
			log.Fatalf("error listing cache: %v", err)
		}
		if len(entries) == 0 {
			fmt.Println("cache is empty")
			return
		}
		for _, id := range entries {
			fmt.Println(id)
		}

	case "clear":
		fs := flag.NewFlagSet("cache clear", flag.ExitOnError)
		id := fs.String("id", "", "media ID to clear (omit to clear all)")
		fs.Parse(args[1:])

		if *id == "" {
			if err := cacheManager.ClearAll(); err != nil {
				log.Fatalf("error clearing cache: %v", err)
			}
			fmt.Println("cache cleared")
		} else {
			if err := cacheManager.Clear(*id); err != nil {
				log.Fatalf("error clearing cache for %s: %v", *id, err)
			}
			fmt.Printf("cache cleared for %s\n", *id)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown cache subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func mustBuildRunner(cfg *config.Config, src source.Source) *pipeline.Runner {
	s := mustLoadStyle(cfg.StyleName)
	articleWriter := claude.New(cfg.AnthropicAPIKey, cfg.ClaudeModel, cfg.TranscriptLimit, s)
	return pipeline.NewRunner(
		src,
		groq.New(cfg.GroqAPIKey),
		articleWriter,
		sanity.New(cfg.SanityProjectID, cfg.SanityDataset, cfg.SanityToken),
		cache.New(cfg.CacheDir),
		state.New(cfg.StateFile),
	)
}

func mustLoadStyle(nameOrPath string) *style.Style {
	s, err := style.Resolve(nameOrPath)
	if err != nil {
		log.Fatalf("style error: %v", err)
	}
	log.Printf("  style: %s", s.Name)
	return s
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
