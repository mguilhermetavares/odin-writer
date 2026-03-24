package groq

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// ---------------------------------------------------------------------------
// Groq tests — fake time via testing/synctest
//
// Why synctest?
//  1. rateLimiter.reserve waits up to 1 hour for the window to reset.
//     Without synctest this scenario simply cannot be tested.
//  2. transcribeFile 429 retry: "Retry-After: 300" would sleep 5 real minutes.
//     With synctest the sleep is instant (fake time advances).
//
// The 429-retry tests use net.Pipe for the HTTP transport so all I/O stays
// inside the bubble (goroutines blocked on net.Pipe reads/writes are durably
// blocked, which is required for fake time to advance correctly).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// rateLimiter tests — no network involved, synctest is straightforward
// ---------------------------------------------------------------------------

// TestRateLimiter_WaitsForWindowReset verifies that when the hourly quota is
// exhausted, reserve blocks until ~1 hour of fake time elapses, then resets
// the window and records the new reservation.
func TestRateLimiter_WaitsForWindowReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := &rateLimiter{windowStart: time.Now()}

		// Pre-fill quota so the next reserve must wait.
		rl.mu.Lock()
		rl.secondsUsed = maxSecondsPerHour
		rl.mu.Unlock()

		ctx := context.Background()
		result := make(chan error, 1)
		go func() {
			result <- rl.reserve(ctx, 100)
		}()

		// Wait until the reserve goroutine is blocked on time.After(~1 h).
		synctest.Wait()

		// Advance fake time by one hour — this unblocks the time.After in reserve.
		time.Sleep(time.Hour)

		if err := <-result; err != nil {
			t.Fatalf("unexpected error from reserve: %v", err)
		}

		rl.mu.Lock()
		used := rl.secondsUsed
		rl.mu.Unlock()

		// After window reset: only the 100 s we reserved should be counted.
		if used != 100 {
			t.Errorf("want secondsUsed=100 after window reset, got %f", used)
		}
	})
}

// TestRateLimiter_CancelDuringWindowWait verifies that cancelling the context
// while reserve is waiting for the window causes reserve to return ctx.Err()
// promptly — without waiting for the full hour.
func TestRateLimiter_CancelDuringWindowWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := &rateLimiter{windowStart: time.Now()}

		rl.mu.Lock()
		rl.secondsUsed = maxSecondsPerHour
		rl.mu.Unlock()

		ctx, cancel := context.WithCancel(context.Background())

		result := make(chan error, 1)
		go func() {
			result <- rl.reserve(ctx, 1)
		}()

		// Wait until reserve goroutine is blocked on select.
		synctest.Wait()

		// Cancel — reserve must return context.Canceled immediately.
		cancel()

		if err := <-result; err != context.Canceled {
			t.Errorf("want context.Canceled, got %v", err)
		}
	})
}

// TestRateLimiter_WindowResetUnblocksQueuedReservation verifies a realistic
// scenario: one goroutine fills the quota, a second goroutine waits, and after
// a fake 1-hour advance the second goroutine completes successfully.
func TestRateLimiter_WindowResetUnblocksQueuedReservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := &rateLimiter{windowStart: time.Now()}
		ctx := context.Background()

		// Fill the quota with the first reservation.
		if err := rl.reserve(ctx, float64(maxSecondsPerHour)); err != nil {
			t.Fatalf("first reserve failed: %v", err)
		}

		// Second reservation must block; run it in a goroutine.
		result := make(chan error, 1)
		go func() {
			result <- rl.reserve(ctx, 3600)
		}()

		synctest.Wait() // second goroutine parked on time.After

		time.Sleep(time.Hour) // advance fake clock by one window

		if err := <-result; err != nil {
			t.Fatalf("second reserve failed after window reset: %v", err)
		}

		rl.mu.Lock()
		used := rl.secondsUsed
		rl.mu.Unlock()

		// Window reset → only the second reservation's 3600 s are counted.
		if used != 3600 {
			t.Errorf("want secondsUsed=3600 after reset, got %f", used)
		}
	})
}

// TestRateLimiter_MultipleReservationsNoWait verifies that consecutive
// reservations that fit within the quota all return immediately (no blocking).
func TestRateLimiter_MultipleReservationsNoWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rl := &rateLimiter{windowStart: time.Now()}
		ctx := context.Background()
		start := time.Now()

		for range 5 {
			if err := rl.reserve(ctx, 1000); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		}

		// No fake time should have advanced — no blocking happened.
		if elapsed := time.Since(start); elapsed > 0 {
			t.Errorf("fake time advanced during non-blocking reserves: %v", elapsed)
		}

		rl.mu.Lock()
		used := rl.secondsUsed
		rl.mu.Unlock()

		if used != 5000 {
			t.Errorf("want secondsUsed=5000, got %f", used)
		}
	})
}

// ---------------------------------------------------------------------------
// transcribeFile 429-retry tests — uses net.Pipe to keep I/O inside bubble
// ---------------------------------------------------------------------------

// pipeServer drives a net.Pipe connection as an HTTP server.
// responses is a list of (statusCode, body) pairs served in order.
type pipeResponse struct {
	code    int
	headers map[string]string
	body    string
}

// newPipeTranscriber returns a Transcriber whose HTTP client talks to an
// in-process net.Pipe server that serves the given responses in order.
// Each call to RoundTrip creates a fresh net.Pipe so that Connection: close
// semantics work correctly across multiple requests (e.g. 429 retry).
func newPipeTranscriber(responses []pipeResponse) *Transcriber {
	return &Transcriber{
		apiKey:      "test-key",
		rateLimiter: newRateLimiter(),
		client: &http.Client{
			Transport: &multiPipeTransport{responses: responses},
		},
	}
}

// multiPipeTransport serves pre-canned responses over fresh net.Pipe pairs,
// one pair per RoundTrip call.  The body is fully buffered before returning
// so callers don't need the underlying connection to remain open.
type multiPipeTransport struct {
	responses []pipeResponse
	idx       int
}

func (t *multiPipeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.idx >= len(t.responses) {
		return nil, fmt.Errorf("multiPipeTransport: no more canned responses (idx=%d)", t.idx)
	}
	r := t.responses[t.idx]
	t.idx++

	srv, cli := net.Pipe()

	// Server side: read request, write response, close.
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		defer srv.Close()
		http.ReadRequest(bufio.NewReader(srv)) //nolint:errcheck

		headers := ""
		for k, v := range r.headers {
			headers += fmt.Sprintf("%s: %s\r\n", k, v)
		}
		fmt.Fprintf(srv,
			"HTTP/1.1 %d %s\r\n%sContent-Length: %d\r\nConnection: close\r\n\r\n%s",
			r.code, http.StatusText(r.code), headers, len(r.body), r.body,
		)
	}()

	// Client side: write request, read response, buffer body, close.
	if err := req.Write(cli); err != nil {
		cli.Close()
		<-serverDone
		return nil, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(cli), req)
	if err != nil {
		cli.Close()
		<-serverDone
		return nil, err
	}

	// Buffer the body so we can close the pipe immediately.
	var buf bytes.Buffer
	io.Copy(&buf, resp.Body) //nolint:errcheck
	resp.Body.Close()
	cli.Close()
	<-serverDone

	resp.Body = io.NopCloser(&buf)
	return resp, nil
}

// audioFileSynctest creates a small temporary audio file and returns its path.
func audioFileSynctest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.webm")
	if err := os.WriteFile(path, []byte("fake audio data"), 0600); err != nil {
		t.Fatalf("creating temp audio file: %v", err)
	}
	return path
}

// TestTranscribeFile_429RetryIsInstant verifies that a Retry-After: 300
// (5-minute wait) from the Groq API is handled via fake time — the test
// completes instantly instead of sleeping 5 real minutes.
func TestTranscribeFile_429RetryIsInstant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		responses := []pipeResponse{
			{
				code:    http.StatusTooManyRequests,
				headers: map[string]string{"Retry-After": "300"},
				body:    `{"error":{"message":"rate limited"}}`,
			},
			{
				code: http.StatusOK,
				body: "transcription after rate limit",
			},
		}
		tr := newPipeTranscriber(responses)
		audioPath := audioFileSynctest(t)
		before := time.Now()

		got, err := tr.transcribeFile(context.Background(), audioPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "transcription after rate limit" {
			t.Errorf("want transcription text, got %q", got)
		}

		// Fake time must have advanced by 300 s (the Retry-After value).
		if elapsed := time.Since(before); elapsed < 300*time.Second {
			t.Errorf("fake elapsed %v < 300 s — 429 sleep may not have fired", elapsed)
		}
	})
}

// TestTranscribeFile_LongRetryAfterInBody verifies that the body-parsed
// duration ("try again in 34m48s") also drives the fake-time advance.
func TestTranscribeFile_LongRetryAfterInBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		expected := time.Duration(34*60+48) * time.Second // 2088 s

		responses := []pipeResponse{
			{
				code: http.StatusTooManyRequests,
				body: `{"error":{"message":"Please try again in 34m48s"}}`,
			},
			{
				code: http.StatusOK,
				body: "ok",
			},
		}
		tr := newPipeTranscriber(responses)
		audioPath := audioFileSynctest(t)
		before := time.Now()

		got, err := tr.transcribeFile(context.Background(), audioPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "ok" {
			t.Errorf("want %q, got %q", "ok", got)
		}

		if elapsed := time.Since(before); elapsed < expected {
			t.Errorf("fake elapsed %v < %v — body-parsed duration not applied",
				elapsed, expected)
		}
	})
}
