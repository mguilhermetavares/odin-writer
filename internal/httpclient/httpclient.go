package httpclient

import (
	"math/rand"
	"net/http"
	"time"
)

const (
	maxRetries = 3
	baseDelay  = 1 * time.Second
	maxDelay   = 30 * time.Second
)

// RetryTransport is an http.RoundTripper that retries on 429 and 5xx responses
// using exponential backoff with jitter.
type RetryTransport struct {
	base http.RoundTripper
}

func New() *http.Client {
	return &http.Client{
		Transport: &RetryTransport{base: http.DefaultTransport},
	}
}

func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoff(attempt)
			time.Sleep(delay)

			// Body must be re-read on each attempt; GetBody is set by http.NewRequest
			// when the body is replayable (bytes.Reader, strings.Reader, etc.).
			if req.GetBody != nil {
				body, berr := req.GetBody()
				if berr != nil {
					return nil, berr
				}
				req.Body = body
			}
		}

		resp, err = t.base.RoundTrip(req)
		if err != nil {
			// Network errors are retried
			continue
		}

		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return resp, nil
		}

		// Close body before retry to avoid connection leaks.
		// On the last attempt, leave the body open for the caller to read.
		if attempt < maxRetries {
			resp.Body.Close()
		}
	}

	if err != nil {
		return nil, err
	}
	return resp, nil
}

func backoff(attempt int) time.Duration {
	delay := baseDelay * time.Duration(1<<uint(attempt-1))
	if delay > maxDelay {
		delay = maxDelay
	}
	// Add ±25% jitter
	jitter := time.Duration(rand.Int63n(int64(delay) / 2))
	return delay + jitter - jitter/2
}
