package transcriber

import "context"

// Transcriber converts an audio file to text.
// durationSec is the total audio duration in seconds (0 if unknown);
// used to respect API rate limits when splitting large files.
type Transcriber interface {
	Transcribe(ctx context.Context, audioPath string, durationSec int) (string, error)
}
