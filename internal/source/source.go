package source

import "context"

// Media represents a piece of media ready to be transcribed.
// AudioPath points to the audio file on disk (may be a temp file for YouTube).
type Media struct {
	ID        string // video ID, file hash, etc.
	Title     string
	AudioPath string
	SourceID  string // "youtube", "localfile"
}

// Options controls how a source fetches media.
type Options struct {
	// VideoID targets a specific YouTube video (optional; defaults to latest).
	VideoID string
	// Path is the local file path (required for localfile source).
	Path string
	// Title overrides the media title (optional for localfile).
	Title string
}

// Source fetches and prepares media (metadata + audio) for transcription.
// Each implementation handles its own download/copy logic internally.
type Source interface {
	Prepare(ctx context.Context, opts Options, destDir string) (*Media, error)
}
