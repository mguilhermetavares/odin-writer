package pipeline

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"odin-writer/internal/cache"
	"odin-writer/internal/publisher"
	"odin-writer/internal/source"
	"odin-writer/internal/state"
	"odin-writer/internal/transcriber"
	"odin-writer/internal/writer"
)

// Runner orchestrates the full pipeline:
// Source → Transcribe → Write → Publish
type Runner struct {
	source      source.Source
	transcriber transcriber.Transcriber
	writer      writer.Writer
	publisher   publisher.Publisher
	cache       *cache.Manager
	state       *state.Manager
}

func NewRunner(
	src source.Source,
	t transcriber.Transcriber,
	w writer.Writer,
	p publisher.Publisher,
	c *cache.Manager,
	s *state.Manager,
) *Runner {
	return &Runner{
		source:      src,
		transcriber: t,
		writer:      w,
		publisher:   p,
		cache:       c,
		state:       s,
	}
}

// Run executes the pipeline according to the provided options.
func (r *Runner) Run(ctx context.Context, opts RunOptions) error {
	log.Println("odin-writer starting")

	tmpDir, err := os.MkdirTemp("", "odin-writer-audio-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	log.Printf("  source: %s", opts.Source)
	media, err := r.source.Prepare(ctx, source.Options{
		VideoID: opts.VideoID,
		Path:    opts.Path,
		Title:   opts.Title,
	}, tmpDir)
	if err != nil {
		return fmt.Errorf("preparing source: %w", err)
	}
	log.Printf("  media: [%s] %s", media.ID, media.Title)

	if !opts.Force {
		processed, err := r.state.WasProcessed(media.ID)
		if err != nil {
			return fmt.Errorf("checking state: %w", err)
		}
		if processed {
			log.Println("nothing to do — already processed. use --force to reprocess.")
			return nil
		}
	}

	transcript, err := r.transcribe(ctx, media, opts)
	if err != nil {
		return err
	}

	article, err := r.generateArticle(ctx, media, transcript, opts)
	if err != nil {
		return err
	}

	log.Println("publishing draft to Sanity...")
	docID, err := r.publisher.CreateDraft(ctx, article, media.ID)
	if err != nil {
		return fmt.Errorf("publishing to Sanity: %w", err)
	}

	if err := r.state.Record(state.Entry{
		SourceID:     media.SourceID,
		MediaID:      media.ID,
		ProcessedAt:  time.Now().UTC(),
		ArticleTitle: article.Title,
	}); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	log.Println("done!")
	log.Printf("  article: %s", article.Title)
	log.Printf("  draft  : %s", docID)
	log.Println("  review : https://minnesotavikingsbr.com/studio")

	return nil
}

func (r *Runner) transcribe(ctx context.Context, media *source.Media, opts RunOptions) (string, error) {
	if !opts.Force {
		transcript, err := r.cache.LoadTranscript(media.ID)
		if err != nil {
			return "", err
		}
		if transcript != "" {
			log.Printf("  transcript loaded from cache (%d chars)", len(transcript))
			return transcript, nil
		}
	}

	log.Printf("  transcribing via Groq Whisper: %s", media.AudioPath)
	transcript, err := r.transcriber.Transcribe(ctx, media.AudioPath)
	if err != nil {
		return "", fmt.Errorf("transcription: %w", err)
	}
	log.Printf("  transcript: %d chars", len(transcript))

	if err := r.cache.SaveTranscript(media.ID, transcript); err != nil {
		log.Printf("warning: failed to cache transcript: %v", err)
	}

	return transcript, nil
}

func (r *Runner) generateArticle(ctx context.Context, media *source.Media, transcript string, opts RunOptions) (*writer.Article, error) {
	if !opts.Force {
		article, err := r.cache.LoadArticle(media.ID)
		if err != nil {
			return nil, err
		}
		if article != nil {
			log.Printf("  article loaded from cache: %s", article.Title)
			return article, nil
		}
	}

	log.Println("  generating article with Claude...")
	article, err := r.writer.GenerateArticle(ctx, transcript, media.Title)
	if err != nil {
		return nil, fmt.Errorf("generating article: %w", err)
	}
	log.Printf("  title: %s", article.Title)

	if err := r.cache.SaveArticle(media.ID, article); err != nil {
		log.Printf("warning: failed to cache article: %v", err)
	}

	return article, nil
}
