package httpclient

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// newTestTransport returns a RetryTransport backed by a real HTTP transport
// so it can talk to httptest servers.
func newTestTransport() *RetryTransport {
	return &RetryTransport{base: http.DefaultTransport}
}

// statusServer starts a test server that always returns the given status code.
func statusServer(t *testing.T, code int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
	}))
}

// countingServer starts a test server that counts calls and returns the
// provided status codes in sequence; the last code is repeated once exhausted.
func countingServer(t *testing.T, codes ...int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1)) - 1
		if n >= len(codes) {
			n = len(codes) - 1
		}
		w.WriteHeader(codes[n])
	}))
	return srv, &calls
}

// doGet performs a GET against url using the provided transport and returns the
// final response (or an error).
func doGet(t *testing.T, rt *RetryTransport, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	return rt.RoundTrip(req)
}

// TestSuccessOnFirstAttemptNoRetry verifies that a 200 response is returned
// immediately without any retry attempts.
func TestSuccessOnFirstAttemptNoRetry(t *testing.T) {
	srv, calls := countingServer(t, http.StatusOK)
	defer srv.Close()

	rt := newTestTransport()
	resp, err := doGet(t, rt, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("want exactly 1 call, got %d", got)
	}
}

// Retry tests (429, 500, 503) are in httpclient_synctest_test.go.
// They use testing/synctest so the backoff sleeps are instant (fake time).

// TestNoRetryOn400 verifies that a 400 is returned immediately without retries.
func TestNoRetryOn400(t *testing.T) {
	srv, calls := countingServer(t, http.StatusBadRequest)
	defer srv.Close()

	rt := newTestTransport()
	resp, err := doGet(t, rt, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("want exactly 1 call, got %d", got)
	}
}

// TestNoRetryOn404 verifies that a 404 is returned immediately without retries.
func TestNoRetryOn404(t *testing.T) {
	srv, calls := countingServer(t, http.StatusNotFound)
	defer srv.Close()

	rt := newTestTransport()
	resp, err := doGet(t, rt, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("want exactly 1 call, got %d", got)
	}
}

// TestGivesUpAfterMaxRetriesOn429 is in httpclient_synctest_test.go.

// TestBackoffGrowsExponentially verifies that successive backoff durations
// increase with each attempt.
func TestBackoffGrowsExponentially(t *testing.T) {
	// Use a large sample size to smooth out jitter effects.
	const samples = 500
	avg := func(attempt int) time.Duration {
		var total time.Duration
		for i := 0; i < samples; i++ {
			total += backoff(attempt)
		}
		return total / samples
	}

	b1 := avg(1)
	b2 := avg(2)
	b3 := avg(3)

	if b1 >= b2 {
		t.Errorf("backoff(1)=%v should be < backoff(2)=%v", b1, b2)
	}
	if b2 >= b3 {
		t.Errorf("backoff(2)=%v should be < backoff(3)=%v", b2, b3)
	}
}

// TestBackoffNeverExceedsMaxDelayWithJitter verifies that the backoff function
// never exceeds maxDelay + 25% jitter for the attempt range used by the
// transport (1..maxRetries). The base exponential delay is capped at maxDelay;
// jitter can add at most 25% (maxDelay/4) on top of that.
func TestBackoffNeverExceedsMaxDelayWithJitter(t *testing.T) {
	// The jitter term is rand.Int63n(delay/2), so the maximum added value is
	// (delay/2 - 1ns), and the formula yields delay + jitter/2. When
	// delay == maxDelay the worst case is maxDelay * 1.25.
	ceiling := maxDelay + maxDelay/4

	for attempt := 1; attempt <= maxRetries; attempt++ {
		for sample := 0; sample < 200; sample++ {
			d := backoff(attempt)
			if d > ceiling {
				t.Errorf("backoff(%d) = %v exceeds ceiling %v (maxDelay=%v + 25%% jitter)",
					attempt, d, ceiling, maxDelay)
			}
		}
	}
}
