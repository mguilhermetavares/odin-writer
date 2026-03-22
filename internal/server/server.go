package server

import (
	"context"
	"log"
	"time"

	"odin-writer/internal/pipeline"
)

// Server polls YouTube for new videos and runs the full pipeline on each tick.
type Server struct {
	runner   *pipeline.Runner
	interval time.Duration
}

func New(runner *pipeline.Runner, interval time.Duration) *Server {
	return &Server{runner: runner, interval: interval}
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
	err := s.runner.Run(ctx, pipeline.RunOptions{
		Source: "youtube",
	})
	if err != nil {
		log.Printf("server: pipeline error: %v", err)
		return
	}
	log.Printf("server: next check in %s", s.interval)
}
