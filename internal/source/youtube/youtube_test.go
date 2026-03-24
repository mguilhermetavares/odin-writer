package youtube

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"odin-writer/internal/source"
)

// ---------------------------------------------------------------------------
// Fake yt-dlp helper
// ---------------------------------------------------------------------------

// installFakeYtDlp writes a shell script that prints fixedOutput to stdout and
// exits with exitCode, then prepends its directory to PATH.
// fixedOutput is written verbatim into the script using a heredoc so that
// tab characters are preserved exactly.
// Returns a cleanup function that restores PATH.
func installFakeYtDlp(t *testing.T, fixedOutput string, exitCode int) func() {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "yt-dlp")

	var body string
	if exitCode == 0 {
		// Write fixedOutput into a file next to the script so we can cat it —
		// this avoids any shell quoting issues with special characters like tabs.
		dataPath := filepath.Join(dir, "output.txt")
		if err := os.WriteFile(dataPath, []byte(fixedOutput), 0o644); err != nil {
			t.Fatalf("installFakeYtDlp: write output file: %v", err)
		}
		body = fmt.Sprintf("#!/bin/sh\ncat %q\nexit 0\n", dataPath)
	} else {
		body = fmt.Sprintf("#!/bin/sh\nexit %d\n", exitCode)
	}

	if err := os.WriteFile(scriptPath, []byte(body), 0o755); err != nil {
		t.Fatalf("installFakeYtDlp: write script: %v", err)
	}

	origPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+origPath); err != nil {
		t.Fatalf("installFakeYtDlp: setenv PATH: %v", err)
	}

	return func() {
		os.Setenv("PATH", origPath)
	}
}

// ---------------------------------------------------------------------------
// fetchLatestFrom tests
// ---------------------------------------------------------------------------

// 1. fetchLatestFrom with valid yt-dlp output parses id, title, uploadDate, durationSec.
func TestFetchLatestFrom_ParsesValidOutput(t *testing.T) {
	restore := installFakeYtDlp(t, "abc123\tGreat Video\t20240315\t3600", 0)
	defer restore()

	s := New("CHAN1")
	meta, err := s.fetchLatestFrom(context.Background(), "https://example.com/playlist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta == nil {
		t.Fatal("expected non-nil meta, got nil")
	}
	if meta.id != "abc123" {
		t.Errorf("id: want %q, got %q", "abc123", meta.id)
	}
	if meta.title != "Great Video" {
		t.Errorf("title: want %q, got %q", "Great Video", meta.title)
	}
	if meta.uploadDate != "20240315" {
		t.Errorf("uploadDate: want %q, got %q", "20240315", meta.uploadDate)
	}
	if meta.durationSec != 3600 {
		t.Errorf("durationSec: want %d, got %d", 3600, meta.durationSec)
	}
}

// 2. fetchLatestFrom with empty output returns nil without error.
func TestFetchLatestFrom_EmptyOutputReturnsNil(t *testing.T) {
	restore := installFakeYtDlp(t, "", 0)
	defer restore()

	s := New("CHAN1")
	meta, err := s.fetchLatestFrom(context.Background(), "https://example.com/playlist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Errorf("expected nil meta for empty output, got %+v", meta)
	}
}

// 3. fetchLatestFrom when command fails returns nil without error.
func TestFetchLatestFrom_CommandFailureReturnsNil(t *testing.T) {
	restore := installFakeYtDlp(t, "", 1)
	defer restore()

	s := New("CHAN1")
	meta, err := s.fetchLatestFrom(context.Background(), "https://example.com/playlist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Errorf("expected nil meta on command failure, got %+v", meta)
	}
}

// ---------------------------------------------------------------------------
// latestVideo selection logic tests (via latestBetween helper)
// ---------------------------------------------------------------------------

// 4. latestVideo chooses live when live.uploadDate > video.uploadDate.
func TestLatestVideo_ChoosesLiveWhenLiveIsNewer(t *testing.T) {
	live := &videoMeta{id: "live1", title: "Live Stream", uploadDate: "20240401", durationSec: 7200}
	video := &videoMeta{id: "vid1", title: "Regular Video", uploadDate: "20240301", durationSec: 3600}

	result, err := latestBetween(video, live)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.id != "live1" {
		t.Errorf("expected live video (id=live1), got %q", result.id)
	}
}

// 5. latestVideo chooses video when video.uploadDate > live.uploadDate.
func TestLatestVideo_ChoosesVideoWhenVideoIsNewer(t *testing.T) {
	live := &videoMeta{id: "live1", title: "Live Stream", uploadDate: "20240201", durationSec: 7200}
	video := &videoMeta{id: "vid1", title: "Regular Video", uploadDate: "20240501", durationSec: 3600}

	result, err := latestBetween(video, live)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.id != "vid1" {
		t.Errorf("expected regular video (id=vid1), got %q", result.id)
	}
}

// 6. latestVideo returns live when video is nil.
func TestLatestVideo_ReturnsLiveWhenVideoIsNil(t *testing.T) {
	live := &videoMeta{id: "live-only", title: "Only Live", uploadDate: "20240401", durationSec: 3600}

	result, err := latestBetween(nil, live)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.id != "live-only" {
		t.Errorf("expected live-only, got %q", result.id)
	}
}

// 7. latestVideo returns video when live is nil.
func TestLatestVideo_ReturnsVideoWhenLiveIsNil(t *testing.T) {
	video := &videoMeta{id: "video-only", title: "Only Video", uploadDate: "20240301", durationSec: 1800}

	result, err := latestBetween(video, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.id != "video-only" {
		t.Errorf("expected video-only, got %q", result.id)
	}
}

// 8. latestVideo returns error when both are nil.
func TestLatestVideo_ErrorWhenBothNil(t *testing.T) {
	_, err := latestBetween(nil, nil)
	if err == nil {
		t.Fatal("expected error when both video and live are nil, got nil")
	}
}

// ---------------------------------------------------------------------------
// videoMetadata tests
// ---------------------------------------------------------------------------

// 9. videoMetadata parses all fields correctly.
func TestVideoMetadata_ParsesAllFields(t *testing.T) {
	restore := installFakeYtDlp(t, "xyz789\tAwesome Episode\t20231201\t5400", 0)
	defer restore()

	s := New("")
	meta, err := s.videoMetadata(context.Background(), "xyz789")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.id != "xyz789" {
		t.Errorf("id: want %q, got %q", "xyz789", meta.id)
	}
	if meta.title != "Awesome Episode" {
		t.Errorf("title: want %q, got %q", "Awesome Episode", meta.title)
	}
	if meta.uploadDate != "20231201" {
		t.Errorf("uploadDate: want %q, got %q", "20231201", meta.uploadDate)
	}
	if meta.durationSec != 5400 {
		t.Errorf("durationSec: want %d, got %d", 5400, meta.durationSec)
	}
}

// 10. videoMetadata returns error when yt-dlp command fails.
func TestVideoMetadata_ReturnsErrorOnCommandFailure(t *testing.T) {
	restore := installFakeYtDlp(t, "", 1)
	defer restore()

	s := New("")
	_, err := s.videoMetadata(context.Background(), "failvid")
	if err == nil {
		t.Fatal("expected error when yt-dlp exits non-zero, got nil")
	}
}

// ---------------------------------------------------------------------------
// Prepare with VideoID — integration test using full fake yt-dlp script
// ---------------------------------------------------------------------------

// 11. Prepare with VideoID set calls videoMetadata and downloads audio.
func TestPrepare_WithVideoID_UsesVideoMetadata(t *testing.T) {
	// The fake yt-dlp must handle both the metadata (--print) and download calls.
	// For the download call, it creates a dummy audio file at the output path.
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "yt-dlp")

	script := `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "--print" ]; then
    printf 'testvid\tTest Video Title\t20240601\t1200'
    exit 0
  fi
done
# No --print flag: simulate download. Find the --output value and write a file.
prev=""
for arg in "$@"; do
  if [ "$prev" = "--output" ]; then
    outfile=$(printf '%s' "$arg" | sed 's/%(ext)s/m4a/')
    touch "$outfile"
    exit 0
  fi
  prev="$arg"
done
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", dir+string(os.PathListSeparator)+origPath)
	defer os.Setenv("PATH", origPath)

	s := New("")
	destDir := t.TempDir()
	opts := source.Options{VideoID: "testvid"}
	media, err := s.Prepare(context.Background(), opts, destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if media.ID != "testvid" {
		t.Errorf("expected ID=testvid, got %q", media.ID)
	}
	if media.Title != "Test Video Title" {
		t.Errorf("expected title=%q, got %q", "Test Video Title", media.Title)
	}
	if media.DurationSec != 1200 {
		t.Errorf("expected durationSec=1200, got %d", media.DurationSec)
	}
	if media.SourceID != "youtube" {
		t.Errorf("expected sourceID=youtube, got %q", media.SourceID)
	}
}

// ---------------------------------------------------------------------------
// Helper: latestBetween mirrors the switch logic in latestVideo
// so we can test the selection logic without touching exec.
// ---------------------------------------------------------------------------

func latestBetween(video, live *videoMeta) (*videoMeta, error) {
	switch {
	case video == nil && live == nil:
		return nil, fmt.Errorf("no videos or streams found")
	case video == nil:
		return live, nil
	case live == nil:
		return video, nil
	default:
		if live.uploadDate > video.uploadDate {
			return live, nil
		}
		return video, nil
	}
}
