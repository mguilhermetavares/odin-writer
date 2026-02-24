package youtube

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"odin-writer/internal/source"
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
	title := opts.VideoID // fallback title

	if videoID == "" {
		if s.channelID == "" {
			return nil, fmt.Errorf("YOUTUBE_CHANNEL_ID is required for auto mode")
		}
		meta, err := s.latestVideo(ctx)
		if err != nil {
			return nil, fmt.Errorf("fetching latest video: %w", err)
		}
		videoID = meta.id
		title = meta.title
	}

	audioPath, err := s.downloadAudio(ctx, videoID, destDir)
	if err != nil {
		return nil, fmt.Errorf("downloading audio for %s: %w", videoID, err)
	}

	return &source.Media{
		ID:        videoID,
		Title:     title,
		AudioPath: audioPath,
		SourceID:  "youtube",
	}, nil
}

type videoMeta struct {
	id    string
	title string
}

func (s *Source) latestVideo(ctx context.Context) (*videoMeta, error) {
	url := "https://www.youtube.com/channel/" + s.channelID + "/videos"
	out, err := exec.CommandContext(ctx,
		"yt-dlp",
		"--playlist-end", "1",
		"--print", "%(id)s\t%(title)s",
		"--no-warnings",
		"--quiet",
		url,
	).Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp: %w", err)
	}

	line := strings.TrimSpace(string(out))
	parts := strings.SplitN(line, "\t", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("unexpected yt-dlp output: %q", line)
	}

	return &videoMeta{id: parts[0], title: parts[1]}, nil
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
