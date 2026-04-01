package notifier

import "context"

// Notifier sends a notification message to an external service.
type Notifier interface {
	Notify(ctx context.Context, msg string) error
}
