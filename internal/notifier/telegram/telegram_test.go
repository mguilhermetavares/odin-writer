package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type capturedRequest struct {
	path        string
	contentType string
	chatID      string
	text        string
}

type telegramMockServer struct {
	srv      *httptest.Server
	Captured capturedRequest
	code     int
}

func newTelegramMock(t *testing.T, code int) *telegramMockServer {
	t.Helper()
	m := &telegramMockServer{code: code}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Captured.path = r.URL.Path
		m.Captured.contentType = r.Header.Get("Content-Type")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err == nil {
			m.Captured.chatID = payload["chat_id"]
			m.Captured.text = payload["text"]
		}

		w.WriteHeader(m.code)
		w.Write([]byte(`{"ok":true}`))
	}))
	return m
}

func notifierFor(t *testing.T, m *telegramMockServer) *Notifier {
	t.Helper()
	return &Notifier{
		token:   "test-token",
		chatID:  "123456",
		client:  m.srv.Client(),
		baseURL: m.srv.URL,
	}
}

func TestTelegram_URLPathContainsToken(t *testing.T) {
	m := newTelegramMock(t, http.StatusOK)
	defer m.srv.Close()

	n := notifierFor(t, m)
	if err := n.Notify(context.Background(), "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(m.Captured.path, "test-token") {
		t.Errorf("path = %q does not contain bot token", m.Captured.path)
	}
	if !strings.HasSuffix(m.Captured.path, "/sendMessage") {
		t.Errorf("path = %q does not end with /sendMessage", m.Captured.path)
	}
}

func TestTelegram_ContentTypeIsJSON(t *testing.T) {
	m := newTelegramMock(t, http.StatusOK)
	defer m.srv.Close()

	if err := notifierFor(t, m).Notify(context.Background(), "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasPrefix(m.Captured.contentType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", m.Captured.contentType)
	}
}

func TestTelegram_ChatIDAndTextInPayload(t *testing.T) {
	m := newTelegramMock(t, http.StatusOK)
	defer m.srv.Close()

	n := notifierFor(t, m)
	msg := "pipeline error: something went wrong"
	if err := n.Notify(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.Captured.chatID != "123456" {
		t.Errorf("chat_id = %q, want %q", m.Captured.chatID, "123456")
	}
	if m.Captured.text != msg {
		t.Errorf("text = %q, want %q", m.Captured.text, msg)
	}
}

func TestTelegram_ErrorOnNonOKResponse(t *testing.T) {
	m := newTelegramMock(t, http.StatusBadRequest)
	defer m.srv.Close()

	err := notifierFor(t, m).Notify(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error on 400 response, got nil")
	}
}
