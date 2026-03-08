package pipeline

// RunOptions controls the behaviour of a pipeline run.
type RunOptions struct {
	// Source selects the media source: "youtube" (default) or "file".
	Source string

	// VideoID targets a specific YouTube video (optional).
	VideoID string

	// Path is the local file path (required for source=file).
	Path string

	// Title overrides the media title (optional for source=file).
	Title string

	// Force ignores state and cache — reprocesses from scratch.
	Force bool

	// DryRun runs the full pipeline but skips publishing to Sanity.
	DryRun bool

	// RewriteOnly loads the transcript from cache, regenerates the article,
	// and skips publishing. Requires a cached transcript.
	RewriteOnly bool
}
