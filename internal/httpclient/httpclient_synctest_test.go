package httpclient

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

// ---------------------------------------------------------------------------
// Retry tests with fake time via testing/synctest
//
// Why synctest?
//   RetryTransport calls time.Sleep(backoff(attempt)) between retries.
//   backoff(1)≈1s, backoff(2)≈2s, backoff(3)≈4s — tests that exhaust retries
//   used to take ~10 s of real wall time.  Inside a synctest bubble those
//   sleeps use a fake clock that advances instantly.
//
// Why net.Pipe instead of httptest.Server?
//   I/O on a real network socket is NOT durably blocking in a synctest bubble,
//   meaning fake time could advance while waiting for I/O.  net.Pipe creates
//   an in-process connection whose reads/writes are coordinated by goroutines
//   inside the bubble, keeping the fake clock well-behaved.
// ---------------------------------------------------------------------------

// pipeTransport is an http.RoundTripper backed by net.Pipe — all I/O stays
// inside the bubble.  handler is called in a goroutine to write the response.
type pipeTransport struct {
	handler func(w http.ResponseWriter, r *http.Request)
}

func (pt *pipeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	srv, cli := net.Pipe()

	// Server side: parse request, call handler, write response.
	go func() {
		defer srv.Close()
		parsedReq, err := http.ReadRequest(bufio.NewReader(srv))
		if err != nil {
			return
		}
		rw := &pipeResponseWriter{conn: srv, header: make(http.Header), code: 200}
		pt.handler(rw, parsedReq)
		rw.flush()
	}()

	// Client side: write request, read response.
	defer cli.Close()
	if err := req.Write(cli); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(cli), req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// pipeResponseWriter is a minimal http.ResponseWriter over a net.Pipe conn.
type pipeResponseWriter struct {
	conn   net.Conn
	header http.Header
	code   int
	body   []byte
}

func (rw *pipeResponseWriter) Header() http.Header        { return rw.header }
func (rw *pipeResponseWriter) WriteHeader(code int)       { rw.code = code }
func (rw *pipeResponseWriter) Write(b []byte) (int, error) {
	rw.body = append(rw.body, b...)
	return len(b), nil
}
func (rw *pipeResponseWriter) flush() {
	body := rw.body
	fmt.Fprintf(rw.conn,
		"HTTP/1.1 %d %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		rw.code, http.StatusText(rw.code), len(body), body,
	)
}

// sequentialHandler returns an http.HandlerFunc that responds with the given
// status codes in order; the last code is repeated once exhausted.
func sequentialHandler(codes ...int) http.HandlerFunc {
	var i int
	return func(w http.ResponseWriter, r *http.Request) {
		code := codes[min(i, len(codes)-1)]
		i++
		w.WriteHeader(code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// newPipeTransport wraps sequentialHandler in a pipeTransport.
func newPipeTransport(codes ...int) *RetryTransport {
	return &RetryTransport{base: &pipeTransport{handler: sequentialHandler(codes...)}}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRetry_429IsNotRetried verifies that a 429 is returned immediately to the
// caller without any backoff sleep — Retry-After is the caller's responsibility.
func TestRetry_429IsNotRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		before := time.Now()
		// Second response would be 200, but it must never be reached.
		rt := newPipeTransport(http.StatusTooManyRequests, http.StatusOK)

		req, _ := http.NewRequest(http.MethodGet, "http://test/", nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("want 429 returned as-is, got %d", resp.StatusCode)
		}
		// No backoff sleep should have fired.
		if elapsed := time.Since(before); elapsed >= baseDelay {
			t.Errorf("fake elapsed %v ≥ baseDelay %v — unexpected retry sleep", elapsed, baseDelay)
		}
	})
}

// TestRetry_500ThenSuccess mirrors TestRetryOn500.
func TestRetry_500ThenSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := newPipeTransport(
			http.StatusInternalServerError,
			http.StatusInternalServerError,
			http.StatusOK,
		)

		req, _ := http.NewRequest(http.MethodGet, "http://test/", nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("want 200 after retries, got %d", resp.StatusCode)
		}
	})
}

// TestRetry_503ThenSuccess mirrors TestRetryOn503.
func TestRetry_503ThenSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := newPipeTransport(http.StatusServiceUnavailable, http.StatusOK)

		req, _ := http.NewRequest(http.MethodGet, "http://test/", nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("want 200 after retries, got %d", resp.StatusCode)
		}
	})
}

// TestRetry_ExhaustsMaxRetries verifies that after maxRetries+1 consecutive 5xx
// responses the transport returns the last error response.
// Without synctest this would sleep backoff(1)+backoff(2)+backoff(3) ≈ 7–10 s.
func TestRetry_ExhaustsMaxRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Build a codes slice of maxRetries+1 copies of 500.
		codes := make([]int, maxRetries+1)
		for i := range codes {
			codes[i] = http.StatusInternalServerError
		}
		rt := newPipeTransport(codes...)

		before := time.Now()
		req, _ := http.NewRequest(http.MethodGet, "http://test/", nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("want 500 after exhausted retries, got %d", resp.StatusCode)
		}
		// Fake time advanced by backoff(1)+backoff(2)+backoff(3) ≥ 7×baseDelay.
		if elapsed := time.Since(before); elapsed < 7*baseDelay {
			t.Errorf("fake elapsed %v < 7×baseDelay — not all backoffs fired", elapsed)
		}
	})
}

// TestRetry_BackoffGrowsPerAttempt verifies that each successive retry
// advances fake time more than the previous one.
func TestRetry_BackoffGrowsPerAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Record fake time after each response by intercepting the pipe handler.
		checkpoints := []time.Time{time.Now()}
		var callIdx int
		handler := func(w http.ResponseWriter, r *http.Request) {
			checkpoints = append(checkpoints, time.Now())
			if callIdx < maxRetries {
				w.WriteHeader(http.StatusInternalServerError)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			callIdx++
		}
		rt := &RetryTransport{base: &pipeTransport{handler: handler}}

		req, _ := http.NewRequest(http.MethodGet, "http://test/", nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		// gaps[0] = time before first attempt → 0 (no sleep before attempt 0).
		// gaps[i] for i>0 = fake time elapsed waiting for attempt i.
		for i := 1; i < len(checkpoints)-1; i++ {
			gap := checkpoints[i+1].Sub(checkpoints[i])
			minExpected := baseDelay * time.Duration(1<<uint(i-1))
			if gap < minExpected {
				t.Errorf("gap after attempt %d = %v, want ≥ %v (backoff(%d) base)",
					i, gap, minExpected, i)
			}
		}
	})
}
