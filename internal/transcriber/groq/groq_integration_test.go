//go:build integration

package groq

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mguilhermetavares/odin-writer/internal/config"
)

// TestIntegration_RealVideoTranscription transcribes MVP #250 (1h34m53s, 83MB)
// to exercise the full splitWebm + Groq pipeline with real data.
//
// Run with:
//   go test -v -tags integration -run TestIntegration_RealVideoTranscription -timeout 30m ./internal/transcriber/groq/
func TestIntegration_RealVideoTranscription(t *testing.T) {
	cfg, err := config.Load("../../../.env")
	if err != nil || cfg.GroqAPIKey == "" {
		t.Skip("GROQ_API_KEY not available")
	}

	audioPath := "testdata/i6TEVgBCxQA.webm"
	info, err := os.Stat(audioPath)
	if err != nil {
		t.Skipf("testdata not found — run: yt-dlp -f bestaudio -o testdata/i6TEVgBCxQA.%%(ext)s https://www.youtube.com/watch?v=i6TEVgBCxQA")
	}

	t.Logf("File:     %s (%.1f MB)", audioPath, float64(info.Size())/(1024*1024))
	t.Logf("Duration: 5693s — MVP #250 Resumo da OFFSEASON dos Vikings!")

	tr := New(cfg.GroqAPIKey)
	start := time.Now()

	text, err := tr.Transcribe(context.Background(), audioPath, 5693)
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}

	elapsed := time.Since(start)
	t.Logf("Elapsed:  %s", elapsed.Round(time.Second))
	t.Logf("Length:   %d chars", len(text))

	if len(text) < 1000 {
		t.Errorf("transcription too short (%d chars)", len(text))
	}
	if !strings.Contains(strings.ToLower(text), "vikings") {
		t.Error("'vikings' not found in transcription — sanity check failed")
	}

	n := len(text)
	if n > 500 { n = 500 }
	fmt.Printf("\n--- first 500 chars ---\n%s\n---\n", text[:n])
}
