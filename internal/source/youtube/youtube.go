package youtube

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mguilhermetavares/odin-writer/internal/source"
)

const (
	audioFormat = "bestaudio[ext=webm]/bestaudio"

	// filteredScanDepth is how many recent items per tab are inspected when a
	// title filter is set, so a matching video is not hidden behind newer ones.
	filteredScanDepth = 10
)

// Source fetches YouTube videos using yt-dlp.
// Requires yt-dlp to be installed on the system.
type Source struct {
	channelID   string
	titleFilter *regexp.Regexp
}

// New returns a YouTube source. titleFilter is optional: when non-nil, auto
// mode only picks videos whose title matches it. Explicit video IDs bypass it.
func New(channelID string, titleFilter *regexp.Regexp) *Source {
	return &Source{channelID: channelID, titleFilter: titleFilter}
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

	return parseMeta(strings.TrimSpace(string(out)))
}

// fetchLatestFrom returns the most recent video from a channel playlist URL
// whose title passes the title filter. Returns nil (no error) if the playlist
// is empty, unavailable, or has no matching video among the inspected items.
func (s *Source) fetchLatestFrom(ctx context.Context, url string) (*videoMeta, error) {
	depth := 1
	if s.titleFilter != nil {
		depth = filteredScanDepth
	}

	out, err := exec.CommandContext(ctx,
		"yt-dlp",
		"--playlist-end", strconv.Itoa(depth),
		"--match-filter", "live_status != is_upcoming",
		"--print", "%(id)s\t%(title)s\t%(upload_date)s\t%(duration)s",
		"--no-warnings",
		"--quiet",
		url,
	).Output()
	if err != nil {
		return nil, nil
	}

	// yt-dlp prints playlist items newest first.
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		meta, err := parseMeta(line)
		if err != nil {
			return nil, err
		}
		if s.titleFilter == nil || s.titleFilter.MatchString(meta.title) {
			return meta, nil
		}
	}
	return nil, nil
}

// parseMeta parses one "id\ttitle\tupload_date\tduration" line from yt-dlp.
func parseMeta(line string) (*videoMeta, error) {
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

// latestVideo returns the most recent content from the channel across all tabs.
func (s *Source) latestVideo(ctx context.Context) (*videoMeta, error) {
	base := "https://www.youtube.com/channel/" + s.channelID

	tabs := []string{"/videos", "/streams", "/podcasts"}
	candidates := make([]*videoMeta, 0, len(tabs))
	for _, tab := range tabs {
		m, err := s.fetchLatestFrom(ctx, base+tab)
		if err != nil {
			return nil, fmt.Errorf("fetching latest from %s: %w", tab, err)
		}
		if m != nil {
			candidates = append(candidates, m)
		}
	}

	if len(candidates) == 0 {
		if s.titleFilter != nil {
			return nil, fmt.Errorf("no videos matching title filter %q in the last %d items of each tab for channel %s",
				s.titleFilter, filteredScanDepth, s.channelID)
		}
		return nil, fmt.Errorf("no videos or streams found for channel %s", s.channelID)
	}

	latest := candidates[0]
	for _, m := range candidates[1:] {
		if m.uploadDate > latest.uploadDate {
			latest = m
		}
	}
	return latest, nil
}

func (s *Source) downloadAudio(ctx context.Context, videoID, destDir string) (string, error) {
	template := filepath.Join(destDir, videoID+".%(ext)s")
	url := "https://www.youtube.com/watch?v=" + videoID

	// Prefer webm: the Groq transcriber can only segment webm files, and
	// plain bestaudio often picks a higher-bitrate m4a for long videos.
	cmd := exec.CommandContext(ctx,
		"yt-dlp",
		"-f", audioFormat,
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
