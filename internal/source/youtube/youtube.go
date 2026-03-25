package youtube

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mguilhermetavares/odin-writer/internal/source"
)

// Source fetches YouTube videos using yt-dlp.
// Requires yt-dlp to be installed on the system.
type Source struct {
	channelID string
}

func New(channelID string) *Source {
	return &Source{channelID: channelID}
}

// Prepare fetches the latest video from the channel and downloads its audio.
// If opts.VideoID is set, it downloads that specific video directly.
func (s *Source) Prepare(ctx context.Context, opts source.Options, destDir string) (*source.Media, error) {
	if err := checkYtDlp(); err != nil {
		return nil, err
	}

	videoID := opts.VideoID
	title := opts.VideoID // fallback: video ID itself
	var durationSec int

	if videoID != "" {
		// Fetch real title and duration for specific video IDs; non-fatal on error
		if meta, err := s.videoMetadata(ctx, videoID); err == nil {
			title = meta.title
			durationSec = meta.durationSec
		}
	} else if videoID == "" {
		if s.channelID == "" {
			return nil, fmt.Errorf("YOUTUBE_CHANNEL_ID is required for auto mode")
		}
		meta, err := s.latestVideo(ctx)
		if err != nil {
			return nil, fmt.Errorf("fetching latest video: %w", err)
		}
		videoID = meta.id
		title = meta.title
		durationSec = meta.durationSec
	}

	audioPath, err := s.downloadAudio(ctx, videoID, destDir)
	if err != nil {
		return nil, fmt.Errorf("downloading audio for %s: %w", videoID, err)
	}

	return &source.Media{
		ID:          videoID,
		Title:       title,
		AudioPath:   audioPath,
		SourceID:    "youtube",
		DurationSec: durationSec,
	}, nil
}

type videoMeta struct {
	id          string
	title       string
	uploadDate  string // YYYYMMDD
	durationSec int    // total duration in seconds (0 if unknown)
}

// videoMetadata fetches metadata for a specific video ID.
func (s *Source) videoMetadata(ctx context.Context, videoID string) (*videoMeta, error) {
	url := "https://www.youtube.com/watch?v=" + videoID
	out, err := exec.CommandContext(ctx,
		"yt-dlp",
		"--print", "%(id)s\t%(title)s\t%(upload_date)s\t%(duration)s",
		"--no-warnings",
		"--quiet",
		"--no-download",
		url,
	).Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp metadata: %w", err)
	}

	line := strings.TrimSpace(string(out))
	parts := strings.SplitN(line, "\t", 4)
	if len(parts) < 2 {
		return nil, fmt.Errorf("unexpected yt-dlp output: %q", line)
	}

	meta := &videoMeta{id: parts[0], title: parts[1]}
	if len(parts) >= 3 {
		meta.uploadDate = parts[2]
	}
	if len(parts) == 4 {
		meta.durationSec, _ = strconv.Atoi(parts[3])
	}
	return meta, nil
}

// fetchLatestFrom returns the most recent video from a channel playlist URL.
// Returns nil (no error) if the playlist is empty or unavailable.
func (s *Source) fetchLatestFrom(ctx context.Context, url string) (*videoMeta, error) {
	out, err := exec.CommandContext(ctx,
		"yt-dlp",
		"--playlist-end", "1",
		"--print", "%(id)s\t%(title)s\t%(upload_date)s\t%(duration)s",
		"--no-warnings",
		"--quiet",
		url,
	).Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return nil, nil
	}

	line := strings.TrimSpace(string(out))
	parts := strings.SplitN(line, "\t", 4)
	if len(parts) < 2 {
		return nil, fmt.Errorf("unexpected yt-dlp output: %q", line)
	}

	meta := &videoMeta{id: parts[0], title: parts[1]}
	if len(parts) >= 3 {
		meta.uploadDate = parts[2]
	}
	if len(parts) == 4 {
		meta.durationSec, _ = strconv.Atoi(parts[3])
	}
	return meta, nil
}

// latestVideo returns the most recent content from the channel — video or live,
// whichever was uploaded most recently.
func (s *Source) latestVideo(ctx context.Context) (*videoMeta, error) {
	base := "https://www.youtube.com/channel/" + s.channelID

	video, err := s.fetchLatestFrom(ctx, base+"/videos")
	if err != nil {
		return nil, fmt.Errorf("fetching latest video: %w", err)
	}

	live, err := s.fetchLatestFrom(ctx, base+"/streams")
	if err != nil {
		return nil, fmt.Errorf("fetching latest stream: %w", err)
	}

	switch {
	case video == nil && live == nil:
		return nil, fmt.Errorf("no videos or streams found for channel %s", s.channelID)
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

func (s *Source) downloadAudio(ctx context.Context, videoID, destDir string) (string, error) {
	template := filepath.Join(destDir, videoID+".%(ext)s")
	url := "https://www.youtube.com/watch?v=" + videoID

	cmd := exec.CommandContext(ctx,
		"yt-dlp",
		"-f", "bestaudio",
		"--output", template,
		"--no-warnings",
		url,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("yt-dlp download: %w\n%s", err, string(out))
	}

	matches, err := filepath.Glob(filepath.Join(destDir, videoID+".*"))
	if err != nil || len(matches) == 0 {
		return "", fmt.Errorf("audio file not found in %s after download", destDir)
	}

	return matches[0], nil
}

func checkYtDlp() error {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return fmt.Errorf("yt-dlp not found in PATH: install it with 'pip install yt-dlp' or from https://github.com/yt-dlp/yt-dlp")
	}
	return nil
}
