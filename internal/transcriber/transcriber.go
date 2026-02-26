package transcriber

import "context"

// Transcriber converts an audio file to text.
type Transcriber interface {
	Transcribe(ctx context.Context, audioPath string) (string, error)
}
