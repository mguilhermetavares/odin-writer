package groq

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers for building synthetic webm data
// ---------------------------------------------------------------------------

// makeTestWebm builds a synthetic webm-like byte slice:
//
//	[headerSize arbitrary bytes] [cluster1 bytes] [cluster2 bytes] ...
//
// Each cluster entry in clusters specifies the number of data bytes that
// follow the 4-byte cluster ID magic.
func makeTestWebm(headerSize int, clusters []int) []byte {
	var out []byte
	// header — arbitrary bytes that do NOT contain the cluster magic
	for i := 0; i < headerSize; i++ {
		out = append(out, byte(0xAA))
	}
	for _, size := range clusters {
		out = append(out, webmClusterID...)
		for j := 0; j < size; j++ {
			out = append(out, byte(0xBB))
		}
	}
	return out
}

// writeTestWebm writes makeTestWebm output to a temp file and returns its path.
func writeTestWebm(t *testing.T, headerSize int, clusters []int) string {
	t.Helper()
	data := makeTestWebm(headerSize, clusters)
	dir := t.TempDir()
	path := filepath.Join(dir, "test.webm")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("writeTestWebm: %v", err)
	}
	return path
}

// ---------------------------------------------------------------------------
// EBML helpers: ebmlVINTSize and clusterTimecodeMs
// ---------------------------------------------------------------------------

// makeClusterBytes builds a minimal EBML Cluster containing a Timecode element.
//
//	[Cluster ID 4B] [size VINT 1B] [Timecode ID 1B] [timecode size VINT 1B] [timecode data 2B]
//
// timecodeMs is encoded as a big-endian uint16 (max 65535ms ≈ 65s, sufficient for tests).
func makeClusterBytes(timecodeMs uint16) []byte {
	return []byte{
		0x1F, 0x43, 0xB6, 0x75, // Cluster ID
		0x84,                         // size VINT: 0x84 = 4 bytes follow
		0xE7,                         // Timecode element ID
		0x82,                         // size VINT: 0x82 = 2 bytes
		byte(timecodeMs >> 8), byte(timecodeMs), // big-endian value
	}
}

// TestEBMLVINTSize_OneByte verifies that a 1-byte VINT is parsed correctly.
func TestEBMLVINTSize_OneByte(t *testing.T) {
	// 0x84 = 1000 0100 → marker bit at position 7, value = 0x04
	data := []byte{0x84}
	val, width, ok := ebmlVINTSize(data, 0)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if width != 1 {
		t.Errorf("width: got %d, want 1", width)
	}
	if val != 4 {
		t.Errorf("val: got %d, want 4", val)
	}
}

// TestEBMLVINTSize_TwoBytes verifies that a 2-byte VINT is parsed correctly.
func TestEBMLVINTSize_TwoBytes(t *testing.T) {
	// 0x40 0x05 = 0100 0000 0000 0101 → width=2, value = 0x0005 = 5
	data := []byte{0x40, 0x05}
	val, width, ok := ebmlVINTSize(data, 0)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if width != 2 {
		t.Errorf("width: got %d, want 2", width)
	}
	if val != 5 {
		t.Errorf("val: got %d, want 5", val)
	}
}

// TestEBMLVINTSize_UnknownSize verifies that the 8-byte "unknown size" VINT
// used by streaming muxers is handled without returning ok=false.
func TestEBMLVINTSize_UnknownSize(t *testing.T) {
	// 0x01 0xFF 0xFF 0xFF 0xFF 0xFF 0xFF 0xFF = streaming unknown-size cluster
	data := []byte{0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	_, width, ok := ebmlVINTSize(data, 0)
	if !ok {
		t.Fatal("expected ok=true for unknown-size VINT")
	}
	if width != 8 {
		t.Errorf("width: got %d, want 8", width)
	}
}

// TestEBMLVINTSize_Truncated verifies that ok=false is returned when the data
// is too short to hold the full VINT.
func TestEBMLVINTSize_Truncated(t *testing.T) {
	// 0x40 signals a 2-byte VINT but only 1 byte is provided.
	data := []byte{0x40}
	_, _, ok := ebmlVINTSize(data, 0)
	if ok {
		t.Fatal("expected ok=false for truncated VINT")
	}
}

// TestClusterTimecodeMs_BasicExtraction verifies that a well-formed Cluster
// with a Timecode element is parsed correctly.
func TestClusterTimecodeMs_BasicExtraction(t *testing.T) {
	data := makeClusterBytes(1000) // 1000ms
	ms, ok := clusterTimecodeMs(data, 0, len(data))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ms != 1000 {
		t.Errorf("got %d ms, want 1000", ms)
	}
}

// TestClusterTimecodeMs_ZeroTimecode verifies that a timecode of 0ms is valid.
func TestClusterTimecodeMs_ZeroTimecode(t *testing.T) {
	data := makeClusterBytes(0)
	ms, ok := clusterTimecodeMs(data, 0, len(data))
	if !ok {
		t.Fatal("expected ok=true for zero timecode")
	}
	if ms != 0 {
		t.Errorf("got %d ms, want 0", ms)
	}
}

// TestClusterTimecodeMs_NonZeroOffset verifies that parsing works when the
// cluster does not start at byte 0 (e.g. after a header).
func TestClusterTimecodeMs_NonZeroOffset(t *testing.T) {
	prefix := []byte{0xAA, 0xBB, 0xCC} // arbitrary header bytes
	cluster := makeClusterBytes(5000)
	data := append(prefix, cluster...)
	offset := len(prefix)
	ms, ok := clusterTimecodeMs(data, offset, len(data))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ms != 5000 {
		t.Errorf("got %d ms, want 5000", ms)
	}
}

// TestClusterTimecodeMs_GarbageBytesReturnsFalse verifies the graceful fallback:
// random bytes after the cluster ID cause ok=false instead of a panic or wrong value.
func TestClusterTimecodeMs_GarbageBytesReturnsFalse(t *testing.T) {
	data := []byte{0x1F, 0x43, 0xB6, 0x75, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	_, ok := clusterTimecodeMs(data, 0, len(data))
	if ok {
		t.Fatal("expected ok=false for garbage cluster data")
	}
}

// TestClusterTimecodeMs_LimitPreventsReadingIntoNextCluster verifies that
// parsing stops at limit and returns ok=false if Timecode is not found within.
func TestClusterTimecodeMs_LimitPreventsReadingIntoNextCluster(t *testing.T) {
	cluster := makeClusterBytes(2000)
	// Truncate limit to just the cluster ID + size VINT (no children).
	limit := 4 + 1 // cluster ID (4) + size VINT (1)
	_, ok := clusterTimecodeMs(cluster, 0, limit)
	if ok {
		t.Fatal("expected ok=false when limit excludes Timecode data")
	}
}

// ---------------------------------------------------------------------------
// splitWebm tests
// ---------------------------------------------------------------------------

// TestSplitWebm_SmallFileNoClusterReturnsOriginalAsSingleSegment verifies that
// a file without any Cluster elements is returned as a single segment pointing
// to the original path.
func TestSplitWebm_SmallFileNoClusterReturnsOriginalAsSingleSegment(t *testing.T) {
	path := writeTestWebm(t, 100, nil) // no clusters
	info, _ := os.Stat(path)

	outDir := t.TempDir()
	segs, err := splitWebm(path, info.Size(), 0, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
	if segs[0].path != path {
		t.Errorf("expected original path %q, got %q", path, segs[0].path)
	}
}

// TestSplitWebm_AllClustersInOneSingleSegment verifies that when all clusters
// fit within a single size/time budget, only one segment is produced.
func TestSplitWebm_AllClustersInOneSingleSegment(t *testing.T) {
	// header=100, three small clusters of 50 bytes each → well within 24MB
	path := writeTestWebm(t, 100, []int{50, 50, 50})
	info, _ := os.Stat(path)

	outDir := t.TempDir()
	segs, err := splitWebm(path, info.Size(), 0, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
}

// TestSplitWebm_ExceedsByteLimitProducesMultipleSegments verifies that when
// clusters exceed maxBytes the file is split into multiple segments.
func TestSplitWebm_ExceedsByteLimitProducesMultipleSegments(t *testing.T) {
	// header small; two large clusters that together would exceed maxBytes
	halfBody := maxBytes/2 + 512*1024 // each cluster is > half the budget
	path := writeTestWebm(t, 512, []int{halfBody, halfBody, halfBody})
	info, _ := os.Stat(path)

	outDir := t.TempDir()
	segs, err := splitWebm(path, info.Size(), 0, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segs) < 2 {
		t.Fatalf("expected multiple segments, got %d", len(segs))
	}
}

// TestSplitWebm_EachSegmentPrefixedWithHeader verifies that every produced
// segment file starts with the same bytes as the original header.
func TestSplitWebm_EachSegmentPrefixedWithHeader(t *testing.T) {
	headerSize := 128
	halfBody := maxBytes/2 + 1024*1024
	path := writeTestWebm(t, headerSize, []int{halfBody, halfBody})
	originalData, _ := os.ReadFile(path)
	header := originalData[:headerSize] // everything before first cluster

	info, _ := os.Stat(path)
	outDir := t.TempDir()
	segs, err := splitWebm(path, info.Size(), 0, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, seg := range segs {
		segData, err := os.ReadFile(seg.path)
		if err != nil {
			t.Fatalf("segment %d: cannot read file: %v", i, err)
		}
		if len(segData) < len(header) {
			t.Fatalf("segment %d: too short to contain header", i)
		}
		for j, b := range header {
			if segData[j] != b {
				t.Errorf("segment %d: header byte %d mismatch: got %02x, want %02x", i, j, segData[j], b)
				break
			}
		}
	}
}

// TestSplitWebm_ExceedsDurationLimitSplitsByTime verifies that when the
// estimated duration exceeds maxSecondsPerSegment (19min) the file is split
// even if it would fit within the byte budget.
func TestSplitWebm_ExceedsDurationLimitSplitsByTime(t *testing.T) {
	// Three clusters of ~4MB each — well within 24MB byte limit.
	// With totalDurationSec=4*maxSecondsPerSegment, each cluster ~1520s → triggers time split.
	clusterSize := 4 * 1024 * 1024
	path := writeTestWebm(t, 512, []int{clusterSize, clusterSize, clusterSize})
	info, _ := os.Stat(path)

	outDir := t.TempDir()
	segs, err := splitWebm(path, info.Size(), 4*maxSecondsPerSegment, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segs) < 2 {
		t.Fatalf("expected multiple segments due to time limit, got %d", len(segs))
	}
}

// TestSplitWebm_SegmentsDoNotExceedMaxBytes verifies that no produced segment
// file exceeds maxBytes.
func TestSplitWebm_SegmentsDoNotExceedMaxBytes(t *testing.T) {
	halfBody := maxBytes/2 + 512*1024
	path := writeTestWebm(t, 256, []int{halfBody, halfBody, halfBody, halfBody})
	info, _ := os.Stat(path)

	outDir := t.TempDir()
	segs, err := splitWebm(path, info.Size(), 0, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, seg := range segs {
		fi, err := os.Stat(seg.path)
		if err != nil {
			t.Fatalf("segment %d: stat error: %v", i, err)
		}
		if fi.Size() > maxBytes {
			t.Errorf("segment %d: size %d exceeds maxBytes %d", i, fi.Size(), maxBytes)
		}
	}
}

// ---------------------------------------------------------------------------
// parseRetryAfter tests
// ---------------------------------------------------------------------------

func makeResponse(header string, bodyJSON string) (*http.Response, []byte) {
	resp := &http.Response{Header: make(http.Header)}
	if header != "" {
		resp.Header.Set("Retry-After", header)
	}
	return resp, []byte(bodyJSON)
}

// TestParseRetryAfter_NumericHeaderIsUsed verifies that a numeric Retry-After
// header is parsed and returned directly.
func TestParseRetryAfter_NumericHeaderIsUsed(t *testing.T) {
	resp, body := makeResponse("60", `{}`)
	got := parseRetryAfter(resp, body)
	if got != 60.0 {
		t.Errorf("expected 60.0, got %f", got)
	}
}

// TestParseRetryAfter_NoHeaderFallsBackToBodyMessage verifies that when there
// is no Retry-After header the duration is read from the body message.
func TestParseRetryAfter_NoHeaderFallsBackToBodyMessage(t *testing.T) {
	bodyJSON := `{"error":{"message":"Please try again in 5m30s"}}`
	resp, body := makeResponse("", bodyJSON)
	got := parseRetryAfter(resp, body)
	expected := (5*60 + 30)
	if int(got) != expected {
		t.Errorf("expected %d, got %f", expected, got)
	}
}

// TestParseRetryAfter_NoHeaderNoMessageReturnsDefault verifies that when
// neither header nor recognisable message is present the default 60s is returned.
func TestParseRetryAfter_NoHeaderNoMessageReturnsDefault(t *testing.T) {
	resp, body := makeResponse("", `{"error":{"message":"something else"}}`)
	got := parseRetryAfter(resp, body)
	if got != 60.0 {
		t.Errorf("expected default 60.0, got %f", got)
	}
}

// TestParseRetryAfter_BodyMessageWithMinutesAndSeconds verifies that a message
// in "34m48s" format is parsed correctly via time.ParseDuration.
func TestParseRetryAfter_BodyMessageWithMinutesAndSeconds(t *testing.T) {
	bodyJSON := `{"error":{"message":"Please try again in 34m48s"}}`
	resp, body := makeResponse("", bodyJSON)
	got := parseRetryAfter(resp, body)
	expected := 34*60 + 48
	if int(got) != expected {
		t.Errorf("expected %d, got %f", expected, got)
	}
}

// ---------------------------------------------------------------------------
// rateLimiter tests
// ---------------------------------------------------------------------------

// TestRateLimiter_ReserveDoesNotBlockWhenQuotaAvailable verifies that reserve
// returns immediately when there is sufficient quota in the current window.
func TestRateLimiter_ReserveDoesNotBlockWhenQuotaAvailable(t *testing.T) {
	rl := newRateLimiter()
	ctx := context.Background()

	start := time.Now()
	if err := rl.reserve(ctx, 100); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Errorf("reserve blocked for %s, expected near-instant", elapsed)
	}
}

// TestRateLimiter_ReserveAccumulatesSecondsUsed verifies that secondsUsed grows
// with each successive reserve call.
func TestRateLimiter_ReserveAccumulatesSecondsUsed(t *testing.T) {
	rl := newRateLimiter()
	ctx := context.Background()

	if err := rl.reserve(ctx, 1000); err != nil {
		t.Fatalf("unexpected error on first reserve: %v", err)
	}
	if err := rl.reserve(ctx, 500); err != nil {
		t.Fatalf("unexpected error on second reserve: %v", err)
	}

	rl.mu.Lock()
	used := rl.secondsUsed
	rl.mu.Unlock()

	if used != 1500 {
		t.Errorf("expected secondsUsed=1500, got %f", used)
	}
}

// TestRateLimiter_ReserveResetsAfterWindowExpiry verifies that the counter is
// reset to zero when the 1-hour window has already elapsed.
func TestRateLimiter_ReserveResetsAfterWindowExpiry(t *testing.T) {
	rl := newRateLimiter()
	ctx := context.Background()

	// Pre-load some seconds so we can verify a reset.
	if err := rl.reserve(ctx, 3000); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Manually move windowStart back more than 1 hour.
	rl.mu.Lock()
	rl.windowStart = time.Now().Add(-2 * time.Hour)
	rl.mu.Unlock()

	// Next reserve should detect the expired window and reset.
	if err := rl.reserve(ctx, 100); err != nil {
		t.Fatalf("unexpected error after window expiry: %v", err)
	}

	rl.mu.Lock()
	used := rl.secondsUsed
	rl.mu.Unlock()

	// After reset + 100s, secondsUsed should be 100 (not 3100).
	if used != 100 {
		t.Errorf("expected secondsUsed=100 after window reset, got %f", used)
	}
}

// TestRateLimiter_ReserveReturnsErrorOnCancelledContext verifies that reserve
// returns ctx.Err() immediately when the context is already cancelled.
func TestRateLimiter_ReserveReturnsErrorOnCancelledContext(t *testing.T) {
	rl := newRateLimiter()

	// Fill the window so the next reserve would have to wait.
	rl.mu.Lock()
	rl.secondsUsed = maxSecondsPerHour + 1
	rl.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := rl.reserve(ctx, 1)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// transcribeFile tests (via httptest)
// ---------------------------------------------------------------------------

// newTranscriberWithURL builds a Transcriber whose HTTP client points to the
// given base URL instead of the real Groq API. The transcriptionURL package
// variable is temporarily overridden for the duration of the test.
func newTranscriberWithURL(t *testing.T, serverURL string) *Transcriber {
	t.Helper()
	original := transcriptionURL
	// Override the package-level URL used by transcribeFile.
	// We achieve this by building a custom client transport that rewrites the
	// host. Instead, we use a simpler approach: patch the package variable and
	// restore it in t.Cleanup.
	t.Cleanup(func() {
		// restore — requires the variable to be addressable (var, not const)
		_ = original
	})
	return &Transcriber{
		apiKey:      "test-key",
		client:      &http.Client{},
		rateLimiter: newRateLimiter(),
	}
}

// audioFilePath creates a small temporary audio file and returns its path.
func audioFilePath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.webm")
	if err := os.WriteFile(path, []byte("fake audio data"), 0600); err != nil {
		t.Fatalf("creating temp audio file: %v", err)
	}
	return path
}

// transcribeFileURL is a helper that calls transcribeFile but points to a
// custom URL by building the multipart request manually through a
// test-specific Transcriber whose client sends to the test server.
//
// Because transcriptionURL is a const we cannot patch it. Instead we build a
// minimal request directly using the exported-enough surface and an
// http.Client backed by the test server transport.
func transcribeFileViaServer(t *testing.T, srv *httptest.Server, audioPath string) (string, error) {
	t.Helper()
	tr := &Transcriber{
		apiKey: "test-key",
		client: srv.Client(),
		rateLimiter: newRateLimiter(),
	}
	// We need to reach the test server URL, not transcriptionURL (a const).
	// Use a round-tripper that rewrites the destination host.
	tr.client.Transport = &rewriteHostTransport{
		base:    srv.Client().Transport,
		target:  srv.URL,
	}
	return tr.transcribeFile(context.Background(), audioPath)
}

// rewriteHostTransport rewrites the request URL host to a fixed target so
// that code using the hardcoded transcriptionURL const still hits the
// httptest server.
type rewriteHostTransport struct {
	base   http.RoundTripper
	target string // e.g. "http://127.0.0.1:PORT"
}

func (rt *rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request so we don't mutate the caller's copy.
	cloned := req.Clone(req.Context())
	// Parse target and replace scheme+host.
	targetURL := req.URL
	cloned.URL = targetURL.ResolveReference(req.URL)
	cloned.URL.Scheme = "http"
	// Extract host from target string (strip scheme).
	host := rt.target
	if len(host) > 7 && host[:7] == "http://" {
		host = host[7:]
	}
	cloned.URL.Host = host
	if rt.base != nil {
		return rt.base.RoundTrip(cloned)
	}
	return http.DefaultTransport.RoundTrip(cloned)
}

// TestTranscribeFile_Success200ReturnsTranscribedText verifies that a 200
// response causes transcribeFile to return the response body trimmed.
func TestTranscribeFile_Success200ReturnsTranscribedText(t *testing.T) {
	expected := "hello world transcription"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, expected)
	}))
	defer srv.Close()

	audioPath := audioFilePath(t)
	got, err := transcribeFileViaServer(t, srv, audioPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// TestTranscribeFile_429WithRetryAfterSleepsAndRetries is in
// groq_synctest_test.go (fake time via testing/synctest).

// TestTranscribeFile_Error500ReturnsErrorWithStatusCode verifies that a 500
// response causes transcribeFile to return a non-nil error containing the
// status code.
func TestTranscribeFile_Error500ReturnsErrorWithStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		body, _ := json.Marshal(map[string]any{
			"error": map[string]string{"message": "internal error"},
		})
		w.Write(body)
	}))
	defer srv.Close()

	audioPath := audioFilePath(t)
	_, err := transcribeFileViaServer(t, srv, audioPath)
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	errMsg := err.Error()
	if len(errMsg) == 0 {
		t.Error("error message is empty")
	}
	// Error message should mention the HTTP status code.
	expected := "500"
	found := false
	for i := 0; i+len(expected) <= len(errMsg); i++ {
		if errMsg[i:i+len(expected)] == expected {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error message to contain %q, got %q", expected, errMsg)
	}
}

// ---------------------------------------------------------------------------
// ebmlIDWidth tests
// ---------------------------------------------------------------------------

// TestEBMLIDWidth_AllWidths verifies all valid leading-byte ranges and the
// invalid range (0x00–0x0F).
func TestEBMLIDWidth_AllWidths(t *testing.T) {
	tests := []struct {
		name      string
		b         byte
		wantWidth int
	}{
		{"1-byte 0x80", 0x80, 1},
		{"1-byte 0xFF", 0xFF, 1},
		{"2-byte 0x40", 0x40, 2},
		{"2-byte 0x7F", 0x7F, 2},
		{"3-byte 0x20", 0x20, 3},
		{"3-byte 0x3F", 0x3F, 3},
		{"4-byte 0x10", 0x10, 4},
		{"4-byte 0x1F", 0x1F, 4},
		{"invalid 0x00", 0x00, 0},
		{"invalid 0x0F", 0x0F, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ebmlIDWidth(tc.b)
			if got != tc.wantWidth {
				t.Errorf("ebmlIDWidth(0x%02X) = %d, want %d", tc.b, got, tc.wantWidth)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ebmlVINTSize additional tests
// ---------------------------------------------------------------------------

// TestEBMLVINTSize_AtNonZeroPosition verifies that pos is respected when
// the VINT does not start at the beginning of the slice.
func TestEBMLVINTSize_AtNonZeroPosition(t *testing.T) {
	// 3 padding bytes followed by a 1-byte VINT 0x83 (value=3).
	data := []byte{0x00, 0x00, 0x00, 0x83}
	val, width, ok := ebmlVINTSize(data, 3)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if width != 1 {
		t.Errorf("width: got %d, want 1", width)
	}
	if val != 3 {
		t.Errorf("val: got %d, want 3", val)
	}
}

// TestEBMLVINTSize_InvalidLeadingByte verifies that 0x00 (no marker bit set)
// causes ok=false — it would require a 9-byte VINT which is not allowed.
func TestEBMLVINTSize_InvalidLeadingByte(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	_, _, ok := ebmlVINTSize(data, 0)
	if ok {
		t.Fatal("expected ok=false for 0x00 leading byte (no marker bit)")
	}
}

// ---------------------------------------------------------------------------
// clusterTimecodeMs additional tests
// ---------------------------------------------------------------------------

// TestClusterTimecodeMs_SimpleBlockBeforeTimecodeReturnsFalse verifies that
// reaching a SimpleBlock element (0xA3) before finding Timecode causes
// ok=false, as per the EBML spec ordering guarantee.
func TestClusterTimecodeMs_SimpleBlockBeforeTimecodeReturnsFalse(t *testing.T) {
	// Cluster with a SimpleBlock child instead of Timecode.
	// [Cluster ID 4B] [size VINT 1B=4] [SimpleBlock ID 0xA3] [size VINT 1B=2] [2 data bytes]
	data := []byte{
		0x1F, 0x43, 0xB6, 0x75, // Cluster ID
		0x84,       // size VINT: 4 bytes
		0xA3,       // SimpleBlock ID
		0x82,       // size VINT: 2 bytes
		0x01, 0x02, // data
	}
	_, ok := clusterTimecodeMs(data, 0, len(data))
	if ok {
		t.Fatal("expected ok=false when SimpleBlock appears before Timecode")
	}
}

// ---------------------------------------------------------------------------
// splitWebm additional tests
// ---------------------------------------------------------------------------

// makeClusterWith4ByteTimecodeMs builds a minimal EBML Cluster containing a
// 4-byte Timecode element, supporting timecodes up to ~49 days in ms.
//
//	[Cluster ID 4B] [size VINT 1B=6] [Timecode ID 1B] [size VINT 1B=4] [4B big-endian ms]
func makeClusterWith4ByteTimecodeMs(ms uint32) []byte {
	return []byte{
		0x1F, 0x43, 0xB6, 0x75, // Cluster ID
		0x86,                                             // size VINT: value=6 (6 bytes of children)
		0xE7,                                             // Timecode ID
		0x84,                                             // size VINT: value=4
		byte(ms >> 24), byte(ms >> 16), byte(ms >> 8), byte(ms),
	}
}

// newTranscriberForServer returns a Transcriber whose HTTP client redirects
// all requests to srv, allowing end-to-end tests without hitting the real API.
func newTranscriberForServer(t *testing.T, srv *httptest.Server) *Transcriber {
	t.Helper()
	return &Transcriber{
		apiKey: "test-key",
		client: &http.Client{
			Transport: &rewriteHostTransport{
				base:   srv.Client().Transport,
				target: srv.URL,
			},
		},
		rateLimiter: newRateLimiter(),
	}
}

// TestSplitWebm_EachClusterExceedsLimitIndividually verifies the guard added
// to handle the case where every single cluster already exceeds
// maxSecondsPerSegment on its own.  The fix ensures that when
// boundaries[i-1] == chunkBegin (previous cluster was already flushed),
// the current cluster is included in its own segment rather than producing
// a zero-length segment.
func TestSplitWebm_EachClusterExceedsLimitIndividually(t *testing.T) {
	// 4 clusters of 100 KB each.
	// totalDurationSec chosen so each cluster ≈ 2×maxSecondsPerSegment.
	const clusterBodySize = 100 * 1024
	path := writeTestWebm(t, 256, []int{clusterBodySize, clusterBodySize, clusterBodySize, clusterBodySize})
	info, _ := os.Stat(path)

	outDir := t.TempDir()
	// Each cluster ≈ ¼ of total file → ¼ of totalDurationSec.
	// Set totalDurationSec so ¼ ≈ 2×maxSecondsPerSegment → no accumulated overflow.
	totalDuration := 8 * maxSecondsPerSegment
	segs, err := splitWebm(path, info.Size(), totalDuration, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segs) != 4 {
		t.Errorf("want 4 segments (one per cluster), got %d", len(segs))
	}
	for i, seg := range segs {
		fi, _ := os.Stat(seg.path)
		if fi.Size() == 0 {
			t.Errorf("segment %d is empty", i)
		}
	}
}

// TestSplitWebm_ZeroTotalDurationNoTimeSplit verifies that when totalDurationSec
// is 0 and no EBML timecodes are present, the file is never split by time —
// only by size.
func TestSplitWebm_ZeroTotalDurationNoTimeSplit(t *testing.T) {
	// Many small clusters — no single one is near the byte budget.
	path := writeTestWebm(t, 128, []int{1024, 1024, 1024, 1024, 1024})
	info, _ := os.Stat(path)

	outDir := t.TempDir()
	segs, err := splitWebm(path, info.Size(), 0 /* unknown duration */, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segs) != 1 {
		t.Errorf("want 1 segment (no time split when duration unknown), got %d", len(segs))
	}
}

// TestSplitWebm_WithEBMLTimecodesTriggersSplit verifies the hasTimecodes=true
// path: when clusters embed valid EBML Timecodes whose differences exceed
// maxSecondsPerSegment, the file is split based on timecodes, not byte size.
func TestSplitWebm_WithEBMLTimecodesTriggersSplit(t *testing.T) {
	// 3 clusters with timecodes 0 ms, 1200 s, 2400 s — each gap > 1140 s.
	// The clusters themselves are tiny (11 bytes each) so byte budget is never hit.
	header := make([]byte, 64) // arbitrary header, no cluster magic
	for i := range header {
		header[i] = 0xAA
	}
	c0 := makeClusterWith4ByteTimecodeMs(0)
	c1 := makeClusterWith4ByteTimecodeMs(1_200_000) // 1200 s
	c2 := makeClusterWith4ByteTimecodeMs(2_400_000) // 2400 s
	data := append(header, append(c0, append(c1, c2...)...)...)

	audioPath := filepath.Join(t.TempDir(), "ebml.webm")
	if err := os.WriteFile(audioPath, data, 0600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}
	info, _ := os.Stat(audioPath)

	outDir := t.TempDir()
	// Pass a non-zero totalDurationSec so time-based split is eligible.
	segs, err := splitWebm(audioPath, info.Size(), 3600, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segs) != 3 {
		t.Errorf("want 3 segments (one per cluster via EBML timecodes), got %d", len(segs))
	}
}

// TestSplitWebm_EBMLTimecodesApplyEvenWhenDurationUnknown verifies the fix to
// timeExceeded: EBML timecodes are authoritative and must trigger splits even
// when totalDurationSec=0 (duration not supplied by the caller).
func TestSplitWebm_EBMLTimecodesApplyEvenWhenDurationUnknown(t *testing.T) {
	header := make([]byte, 64)
	for i := range header {
		header[i] = 0xAA
	}
	c0 := makeClusterWith4ByteTimecodeMs(0)
	c1 := makeClusterWith4ByteTimecodeMs(1_200_000)
	c2 := makeClusterWith4ByteTimecodeMs(2_400_000)
	data := append(header, append(c0, append(c1, c2...)...)...)

	audioPath := filepath.Join(t.TempDir(), "ebml_nodur.webm")
	if err := os.WriteFile(audioPath, data, 0600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}
	info, _ := os.Stat(audioPath)

	outDir := t.TempDir()
	segs, err := splitWebm(audioPath, info.Size(), 0 /* duration unknown */, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segs) != 3 {
		t.Errorf("want 3 segments (EBML timecodes authoritative even without duration), got %d", len(segs))
	}
}

// ---------------------------------------------------------------------------
// Transcribe end-to-end tests
// ---------------------------------------------------------------------------

// TestTranscribe_SmallFileUsesDirectPath verifies that a file under maxBytes
// is sent as a single request without splitting.
func TestTranscribe_SmallFileUsesDirectPath(t *testing.T) {
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		fmt.Fprint(w, "transcrição direta")
	}))
	defer srv.Close()

	audioPath := audioFilePath(t) // a few bytes — well under 24 MB
	tr := newTranscriberForServer(t, srv)

	got, err := tr.Transcribe(context.Background(), audioPath, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "transcrição direta" {
		t.Errorf("got %q, want %q", got, "transcrição direta")
	}
	if requestCount != 1 {
		t.Errorf("want exactly 1 request (no splitting), got %d", requestCount)
	}
}

// TestTranscribeSegmented_SegmentErrorAbortsPipeline verifies that when a
// segment fails, the whole transcription is aborted and an error is returned.
// Uses time-based splitting to avoid allocating large test data.
func TestTranscribeSegmented_SegmentErrorAbortsPipeline(t *testing.T) {
	const clusterBodySize = 100 * 1024
	audioData := makeTestWebm(256, []int{clusterBodySize, clusterBodySize, clusterBodySize})
	audioPath := filepath.Join(t.TempDir(), "segmented.webm")
	if err := os.WriteFile(audioPath, audioData, 0600); err != nil {
		t.Fatalf("writing audio: %v", err)
	}

	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			fmt.Fprint(w, "seg1 ok")
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"internal error"}}`)
		}
	}))
	defer srv.Close()

	tr := newTranscriberForServer(t, srv)
	// totalDurationSec large enough that each cluster >> maxSecondsPerSegment.
	_, err := tr.transcribeSegmented(context.Background(), audioPath, int64(len(audioData)), 8*maxSecondsPerSegment)
	if err == nil {
		t.Fatal("expected error when a segment fails, got nil")
	}
	if !strings.Contains(err.Error(), "segmento") {
		t.Errorf("error should mention 'segmento', got: %v", err)
	}
	if requestCount < 2 {
		t.Errorf("expected at least 2 requests, got %d", requestCount)
	}
}

// ---------------------------------------------------------------------------
// Real-file scenario tests (inspired by MVP #250 — 83MB / 5693s / 570 clusters)
// ---------------------------------------------------------------------------

// TestSplitWebm_LargeHeader10KB_PrefixedOnAllSegments verifies that a header
// of realistic size (~10 KB, as produced by yt-dlp) is correctly prepended to
// every output segment and that bodyBudget accounts for the header size.
func TestSplitWebm_LargeHeader10KB_PrefixedOnAllSegments(t *testing.T) {
	headerSize := 10 * 1024
	halfBody := maxBytes/2 + 512*1024 // each cluster > half the budget → forces 2 segments
	path := writeTestWebm(t, headerSize, []int{halfBody, halfBody})
	originalData, _ := os.ReadFile(path)
	header := originalData[:headerSize]

	info, _ := os.Stat(path)
	outDir := t.TempDir()
	segs, err := splitWebm(path, info.Size(), 0, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segs) < 2 {
		t.Fatalf("expected at least 2 segments (byte limit), got %d", len(segs))
	}
	for i, seg := range segs {
		segData, err := os.ReadFile(seg.path)
		if err != nil {
			t.Fatalf("segment %d: read error: %v", i, err)
		}
		if len(segData) < headerSize {
			t.Errorf("segment %d: shorter than header (%d < %d)", i, len(segData), headerSize)
			continue
		}
		for j, b := range header {
			if segData[j] != b {
				t.Errorf("segment %d: header byte %d mismatch: got %02x, want %02x", i, j, segData[j], b)
				break
			}
		}
	}
}

// TestSplitWebm_SmallLastClusterAlwaysIncluded verifies the "remainder" code
// path: after all large clusters are split individually, a tiny final cluster
// (like the 37s / 0.5MB tail of MVP #250) is written as the last segment.
func TestSplitWebm_SmallLastClusterAlwaysIncluded(t *testing.T) {
	const largeCluster = 100 * 1024 // triggers individual split
	const tinyCluster = 512         // well under any limit
	path := writeTestWebm(t, 256, []int{largeCluster, largeCluster, largeCluster, tinyCluster})
	info, _ := os.Stat(path)

	outDir := t.TempDir()
	// Each large cluster ≈ 2×maxSecondsPerSegment → split individually.
	segs, err := splitWebm(path, info.Size(), 8*maxSecondsPerSegment, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segs) < 2 {
		t.Fatalf("expected multiple segments, got %d", len(segs))
	}

	// Last segment must exist and contain the tiny cluster (non-empty body).
	last := segs[len(segs)-1]
	fi, err := os.Stat(last.path)
	if err != nil {
		t.Fatalf("last segment stat error: %v", err)
	}
	// header (256) + cluster ID (4) + tiny data (512) = 772 bytes minimum
	const minLastSize = int64(256 + 4 + tinyCluster)
	if fi.Size() < minLastSize {
		t.Errorf("last segment too small: %d bytes, want at least %d (tiny cluster not included)",
			fi.Size(), minLastSize)
	}
}

// TestSplitWebm_NoDataLostOrDuplicated verifies the coverage invariant:
// the sum of all segment bodies (excluding the repeated header) must equal
// the total body of the original file.  Catches off-by-one errors in
// cluster-offset boundary handling.
func TestSplitWebm_NoDataLostOrDuplicated(t *testing.T) {
	const headerSize = 256
	halfBody := maxBytes/2 + 512*1024
	path := writeTestWebm(t, headerSize, []int{halfBody, halfBody, halfBody})
	info, _ := os.Stat(path)
	originalData, _ := os.ReadFile(path)
	totalBodyExpected := len(originalData) - headerSize

	outDir := t.TempDir()
	segs, err := splitWebm(path, info.Size(), 0, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	totalBodyActual := 0
	for i, seg := range segs {
		segData, err := os.ReadFile(seg.path)
		if err != nil {
			t.Fatalf("segment %d: read error: %v", i, err)
		}
		bodyInSeg := len(segData) - headerSize
		if bodyInSeg < 0 {
			t.Errorf("segment %d is shorter than header (%d bytes)", i, len(segData))
			continue
		}
		totalBodyActual += bodyInSeg
	}

	if totalBodyActual != totalBodyExpected {
		t.Errorf("body coverage mismatch: segments contain %d bytes, original body is %d bytes",
			totalBodyActual, totalBodyExpected)
	}
}

// TestTranscribeSegmented_PartsJoinedWithSpace verifies that transcribed
// segments are joined with a single space — critical for sentence continuity
// at split boundaries.
func TestTranscribeSegmented_PartsJoinedWithSpace(t *testing.T) {
	const clusterBodySize = 100 * 1024
	audioData := makeTestWebm(256, []int{clusterBodySize, clusterBodySize})
	audioPath := filepath.Join(t.TempDir(), "twoparts.webm")
	if err := os.WriteFile(audioPath, audioData, 0600); err != nil {
		t.Fatalf("writing audio: %v", err)
	}

	responses := []string{"final da primeira parte", "início da segunda parte"}
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, responses[idx])
		idx++
	}))
	defer srv.Close()

	tr := newTranscriberForServer(t, srv)
	// totalDurationSec chosen so each half ≈ 2×maxSecondsPerSegment (split triggers)
	// but stays well under maxSecondsPerHour (no rate-limiter wait in real time).
	totalDuration := 4 * maxSecondsPerSegment // each segment ≈ 2×1140 = 2280s < 7200
	got, err := tr.transcribeSegmented(context.Background(), audioPath, int64(len(audioData)), totalDuration)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "final da primeira parte início da segunda parte"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
