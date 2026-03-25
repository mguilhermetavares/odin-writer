package groq

// Mock Groq Whisper API server with full request validation.
//
// The real Groq API expects:
//   POST /openai/v1/audio/transcriptions
//   Authorization: Bearer <key>
//   Content-Type: multipart/form-data
//   Fields: model, language, response_format, file
//
// The tests below exercise transcribeFile via transcribeFileViaServer (defined
// in groq_test.go), which routes requests through the rewriteHostTransport.

import (
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// capturedGroqRequest holds every field the mock handler parsed.
type capturedGroqRequest struct {
	method     string
	authHeader string
	model      string
	language   string
	format     string
	audioBytes []byte
}

// groqMockServer is an httptest.Server that validates incoming Groq API requests.
type groqMockServer struct {
	srv      *httptest.Server
	Captured capturedGroqRequest
	code     int
	body     string
}

func newGroqMock(t *testing.T, code int, body string) *groqMockServer {
	t.Helper()
	m := &groqMockServer{code: code, body: body}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Captured.method = r.Method
		m.Captured.authHeader = r.Header.Get("Authorization")

		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			t.Errorf("expected multipart Content-Type, got %q", r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			data, _ := io.ReadAll(part)
			switch part.FormName() {
			case "model":
				m.Captured.model = string(data)
			case "language":
				m.Captured.language = string(data)
			case "response_format":
				m.Captured.format = string(data)
			case "file":
				m.Captured.audioBytes = append([]byte(nil), data...)
			}
		}

		w.WriteHeader(m.code)
		fmt.Fprint(w, m.body)
	}))
	return m
}

// audioFileWithContent writes specific bytes to a temp file and returns its path.
func audioFileWithContent(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audio.webm")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("creating audio file: %v", err)
	}
	return path
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestGroq_SendsCorrectModel verifies that transcribeFile sends model=whisper-large-v3.
func TestGroq_SendsCorrectModel(t *testing.T) {
	mock := newGroqMock(t, http.StatusOK, "transcrição ok")
	defer mock.srv.Close()

	_, err := transcribeFileViaServer(t, mock.srv, audioFilePath(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := mock.Captured.model, "whisper-large-v3"; got != want {
		t.Errorf("model = %q, want %q", got, want)
	}
}

// TestGroq_SendsPortugueseLanguage verifies that language=pt is sent.
func TestGroq_SendsPortugueseLanguage(t *testing.T) {
	mock := newGroqMock(t, http.StatusOK, "ok")
	defer mock.srv.Close()

	_, err := transcribeFileViaServer(t, mock.srv, audioFilePath(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := mock.Captured.language, "pt"; got != want {
		t.Errorf("language = %q, want %q", got, want)
	}
}

// TestGroq_SendsTextResponseFormat verifies that response_format=text is sent.
func TestGroq_SendsTextResponseFormat(t *testing.T) {
	mock := newGroqMock(t, http.StatusOK, "ok")
	defer mock.srv.Close()

	_, err := transcribeFileViaServer(t, mock.srv, audioFilePath(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := mock.Captured.format, "text"; got != want {
		t.Errorf("response_format = %q, want %q", got, want)
	}
}

// TestGroq_UploadsAudioFileBytes verifies that the raw audio bytes are sent in
// the "file" multipart field.
func TestGroq_UploadsAudioFileBytes(t *testing.T) {
	audioContent := []byte("fake-audio-bytes-xyz-1234")
	path := audioFileWithContent(t, audioContent)

	mock := newGroqMock(t, http.StatusOK, "ok")
	defer mock.srv.Close()

	_, err := transcribeFileViaServer(t, mock.srv, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(mock.Captured.audioBytes) != string(audioContent) {
		t.Errorf("audio bytes: got %d bytes (%q), want %d bytes (%q)",
			len(mock.Captured.audioBytes), mock.Captured.audioBytes[:min(10, len(mock.Captured.audioBytes))],
			len(audioContent), audioContent)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestGroq_SendsBearerAuth verifies that the Authorization header uses Bearer.
func TestGroq_SendsBearerAuth(t *testing.T) {
	mock := newGroqMock(t, http.StatusOK, "ok")
	defer mock.srv.Close()

	_, err := transcribeFileViaServer(t, mock.srv, audioFilePath(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasPrefix(mock.Captured.authHeader, "Bearer ") {
		t.Errorf("Authorization = %q, want prefix %q", mock.Captured.authHeader, "Bearer ")
	}
}

// TestGroq_UsesPostMethod verifies that the request uses POST.
func TestGroq_UsesPostMethod(t *testing.T) {
	mock := newGroqMock(t, http.StatusOK, "ok")
	defer mock.srv.Close()

	_, err := transcribeFileViaServer(t, mock.srv, audioFilePath(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.Captured.method != http.MethodPost {
		t.Errorf("method = %q, want POST", mock.Captured.method)
	}
}

// TestGroq_401ReturnsError verifies that HTTP 401 produces an error containing
// the status code.
func TestGroq_401ReturnsError(t *testing.T) {
	mock := newGroqMock(t, http.StatusUnauthorized,
		`{"error":{"message":"invalid api key"}}`)
	defer mock.srv.Close()

	_, err := transcribeFileViaServer(t, mock.srv, audioFilePath(t))
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error %q does not mention 401", err.Error())
	}
}

// TestGroq_403ReturnsError verifies that HTTP 403 produces an error.
func TestGroq_403ReturnsError(t *testing.T) {
	mock := newGroqMock(t, http.StatusForbidden,
		`{"error":{"message":"forbidden"}}`)
	defer mock.srv.Close()

	_, err := transcribeFileViaServer(t, mock.srv, audioFilePath(t))
	if err == nil {
		t.Fatal("expected error for 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error %q does not mention 403", err.Error())
	}
}

// TestGroq_ResponseTextIsTrimmed verifies that surrounding whitespace in the
// API response is stripped before returning.
func TestGroq_ResponseTextIsTrimmed(t *testing.T) {
	mock := newGroqMock(t, http.StatusOK, "  vikings venceram  \n")
	defer mock.srv.Close()

	got, err := transcribeFileViaServer(t, mock.srv, audioFilePath(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "vikings venceram" {
		t.Errorf("got %q, want %q (trimmed)", got, "vikings venceram")
	}
}
