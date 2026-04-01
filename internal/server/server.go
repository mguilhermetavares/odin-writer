package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mguilhermetavares/odin-writer/internal/notifier"
	"github.com/mguilhermetavares/odin-writer/internal/pipeline"
)

// Server polls YouTube for new videos and runs the full pipeline on each tick.
type Server struct {
	runner   *pipeline.Runner
	interval time.Duration
	notifier notifier.Notifier // nil means no notifications
}

func New(runner *pipeline.Runner, interval time.Duration) *Server {
	return &Server{runner: runner, interval: interval}
}

// WithNotifier attaches a notifier that is called on pipeline errors and successes.
func (s *Server) WithNotifier(n notifier.Notifier) *Server {
	s.notifier = n
	return s
}

// Run starts the polling loop. It runs the pipeline immediately, then waits
// for the next tick. Blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) {
	log.Printf("server: starting — poll interval %s", s.interval)

	s.tick(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.tick(ctx)
		case <-ctx.Done():
			log.Println("server: shutting down")
			return
		}
	}
}

func (s *Server) tick(ctx context.Context) {
	log.Println("server: checking for new videos...")
	result, err := s.runner.Run(ctx, pipeline.RunOptions{
		Source: "youtube",
	})
	if err != nil {
		log.Printf("server: pipeline error: %v", err)
		s.notify(ctx, fmt.Sprintf("odin-writer pipeline error: %v", err))
		return
	}
	log.Printf("server: next check in %s", s.interval)
	if !result.Skipped {
		s.notify(ctx, fmt.Sprintf("odin-writer: article published — %s", result.ArticleTitle))
	}
}

func (s *Server) notify(ctx context.Context, msg string) {
	if s.notifier == nil {
		return
	}
	if err := s.notifier.Notify(ctx, msg); err != nil {
		log.Printf("server: failed to send notification: %v", err)
	}
}
