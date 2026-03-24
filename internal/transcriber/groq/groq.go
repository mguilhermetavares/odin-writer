package groq

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"odin-writer/internal/httpclient"
)

const (
	transcriptionURL  = "https://api.groq.com/openai/v1/audio/transcriptions"
	maxBytes          = 24 * 1024 * 1024 // 24MB — margem abaixo do limite de 25MB do Groq
	maxSecondsPerHour = 7200             // limite de segundos de áudio por hora do Groq
)

// webmClusterID é o magic bytes do elemento Cluster no formato webm/EBML.
var webmClusterID = []byte{0x1f, 0x43, 0xb6, 0x75}

// Transcriber envia áudio para a API Groq Whisper.
type Transcriber struct {
	apiKey      string
	client      *http.Client
	rateLimiter *rateLimiter
}

func New(apiKey string) *Transcriber {
	return &Transcriber{
		apiKey:      apiKey,
		client:      httpclient.New(),
		rateLimiter: newRateLimiter(),
	}
}

// Transcribe transcreve o áudio em audioPath.
// Se o ficheiro exceder 24MB ou se a duração total exceder 7200s,
// divide em segmentos respeitando ambos os limites.
// durationSec é a duração total do áudio em segundos (0 se desconhecida).
func (t *Transcriber) Transcribe(ctx context.Context, audioPath string, durationSec int) (string, error) {
	info, err := os.Stat(audioPath)
	if err != nil {
		return "", fmt.Errorf("stat audio file: %w", err)
	}

	if info.Size() <= maxBytes {
		return t.transcribeFile(ctx, audioPath)
	}

	return t.transcribeSegmented(ctx, audioPath, info.Size(), durationSec)
}

// transcribeSegmented divide o ficheiro em segmentos e transcreve cada um sequencialmente.
func (t *Transcriber) transcribeSegmented(ctx context.Context, audioPath string, totalSize int64, totalDurationSec int) (string, error) {
	tmpDir, err := os.MkdirTemp("", "odin-writer-segments-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	segments, err := splitWebm(audioPath, totalSize, totalDurationSec, tmpDir)
	if err != nil {
		return "", fmt.Errorf("splitting audio: %w", err)
	}

	fmt.Printf("  %d segmentos criados\n", len(segments))

	parts := make([]string, len(segments))
	for i, seg := range segments {
		fmt.Printf("  transcrevendo segmento %d/%d (%.1fs estimados)...\n", i+1, len(segments), seg.estimatedSecs)

		if err := t.rateLimiter.reserve(ctx, seg.estimatedSecs); err != nil {
			return "", err
		}

		text, err := t.transcribeFile(ctx, seg.path)
		if err != nil {
			// Erro não-recuperável num segmento: marcar lacuna e continuar
			fmt.Printf("  [AVISO] segmento %d falhou: %v — continuando\n", i+1, err)
			parts[i] = fmt.Sprintf("[segmento %d indisponível]", i+1)
			continue
		}
		parts[i] = text
	}

	return strings.Join(parts, " "), nil
}

// segment representa um ficheiro de segmento de áudio com a sua duração estimada.
type segment struct {
	path          string
	estimatedSecs float64
}

// splitWebm divide um ficheiro webm em chunks válidos sem ffmpeg.
//
// Cada chunk é cortado nos limites dos elementos Cluster do formato webm,
// respeitando dois limites:
//   - tamanho máximo: maxBytes (24MB) menos o tamanho do header
//   - duração máxima: maxSecondsPerHour (7200s) por segmento
//
// O header original (EBML + Tracks + Info) é prefixado em cada chunk
// para que o Whisper consiga decodificar cada segmento independentemente.
func splitWebm(audioPath string, totalSize int64, totalDurationSec int, outDir string) ([]segment, error) {
	data, err := os.ReadFile(audioPath)
	if err != nil {
		return nil, fmt.Errorf("reading audio file: %w", err)
	}

	// Localizar todos os offsets dos elementos Cluster
	var clusterOffsets []int
	pos := 0
	for {
		idx := bytes.Index(data[pos:], webmClusterID)
		if idx == -1 {
			break
		}
		clusterOffsets = append(clusterOffsets, pos+idx)
		pos += idx + 4
	}

	if len(clusterOffsets) == 0 {
		// Formato não reconhecido — devolver como segmento único
		return []segment{{path: audioPath, estimatedSecs: float64(totalDurationSec)}}, nil
	}

	// Header = tudo antes do primeiro Cluster
	header := data[:clusterOffsets[0]]
	bodyBudget := maxBytes - len(header) // bytes de body disponíveis por chunk

	// Duração por byte (para estimar a duração de cada segmento)
	var secsPerByte float64
	if totalDurationSec > 0 && totalSize > 0 {
		secsPerByte = float64(totalDurationSec) / float64(totalSize)
	}

	// Agrupar clusters em segmentos respeitando bodyBudget e maxSecondsPerHour
	var segments []segment
	segIdx := 0
	chunkBegin := clusterOffsets[0]
	boundaries := make([]int, len(clusterOffsets)-1+1)
	copy(boundaries, clusterOffsets[1:])
	boundaries[len(boundaries)-1] = len(data)

	for i, nextCluster := range boundaries {
		bodySize := nextCluster - chunkBegin
		estimatedSecs := float64(bodySize) * secsPerByte

		sizeExceeded := bodySize >= bodyBudget
		timeExceeded := totalDurationSec > 0 && estimatedSecs >= maxSecondsPerHour

		if sizeExceeded || timeExceeded {
			// Flush até ao cluster anterior (não inclui nextCluster)
			var flushEnd int
			if i > 0 {
				flushEnd = boundaries[i-1]
			} else {
				flushEnd = nextCluster // cluster único já excede o limite — incluir na mesma
			}

			seg, err := writeSegment(data, header, chunkBegin, flushEnd, outDir, segIdx)
			if err != nil {
				return nil, err
			}
			seg.estimatedSecs = float64(flushEnd-chunkBegin) * secsPerByte
			segments = append(segments, seg)
			segIdx++
			chunkBegin = flushEnd
		}
	}

	// Último chunk (dados restantes)
	if chunkBegin < len(data) {
		seg, err := writeSegment(data, header, chunkBegin, len(data), outDir, segIdx)
		if err != nil {
			return nil, err
		}
		seg.estimatedSecs = float64(len(data)-chunkBegin) * secsPerByte
		segments = append(segments, seg)
	}

	return segments, nil
}

// writeSegment escreve header + data[begin:end] num ficheiro temporário.
func writeSegment(data, header []byte, begin, end int, outDir string, idx int) (segment, error) {
	path := filepath.Join(outDir, fmt.Sprintf("seg%03d.webm", idx))
	content := make([]byte, 0, len(header)+(end-begin))
	content = append(content, header...)
	content = append(content, data[begin:end]...)
	if err := os.WriteFile(path, content, 0600); err != nil {
		return segment{}, fmt.Errorf("writing segment %d: %w", idx, err)
	}
	return segment{path: path}, nil
}

// transcribeFile envia um único ficheiro de áudio à API Groq Whisper.
func (t *Transcriber) transcribeFile(ctx context.Context, audioPath string) (string, error) {
	f, err := os.Open(audioPath)
	if err != nil {
		return "", fmt.Errorf("open audio: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	if err := w.WriteField("model", "whisper-large-v3"); err != nil {
		return "", err
	}
	if err := w.WriteField("language", "pt"); err != nil {
		return "", err
	}
	if err := w.WriteField("response_format", "text"); err != nil {
		return "", err
	}

	part, err := w.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, transcriptionURL, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("groq request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		waitSecs := parseRetryAfter(resp, body)
		fmt.Printf("  rate limit atingido — aguardando %.0fs...\n", waitSecs)
		select {
		case <-time.After(time.Duration(waitSecs) * time.Second):
		case <-ctx.Done():
			return "", ctx.Err()
		}
		// Retry único após espera
		return t.transcribeFile(ctx, audioPath)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("groq API error %d: %s", resp.StatusCode, groqErrorMessage(body))
	}

	return strings.TrimSpace(string(body)), nil
}

// parseRetryAfter extrai o tempo de espera em segundos de uma resposta 429.
// Tenta o header Retry-After primeiro, depois a mensagem de erro do Groq.
func parseRetryAfter(resp *http.Response, body []byte) float64 {
	const defaultWait = 60.0

	if h := resp.Header.Get("Retry-After"); h != "" {
		if secs, err := strconv.ParseFloat(h, 64); err == nil {
			return secs
		}
	}

	// Mensagem do Groq: "Please try again in 34m48s"
	msg := groqErrorMessage(body)
	if idx := strings.Index(msg, "try again in "); idx != -1 {
		raw := msg[idx+len("try again in "):]
		raw = strings.Fields(raw)[0] // ex: "34m48s"
		if d, err := time.ParseDuration(raw); err == nil {
			return d.Seconds()
		}
	}

	return defaultWait
}

// rateLimiter controla o consumo de segundos de áudio por hora (in-memory).
type rateLimiter struct {
	mu          sync.Mutex
	windowStart time.Time
	secondsUsed float64
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{windowStart: time.Now()}
}

// reserve aguarda se necessário para garantir que o próximo segmento
// cabe na janela de 1 hora. Reseta o contador se a janela expirou.
func (r *rateLimiter) reserve(ctx context.Context, secs float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if time.Since(r.windowStart) >= time.Hour {
		r.windowStart = time.Now()
		r.secondsUsed = 0
	}

	if r.secondsUsed+secs > maxSecondsPerHour {
		waitUntil := r.windowStart.Add(time.Hour)
		waitDuration := time.Until(waitUntil)
		fmt.Printf("  limite de %ds/hora atingido — aguardando %s...\n", maxSecondsPerHour, waitDuration.Round(time.Second))
		r.mu.Unlock()
		select {
		case <-time.After(waitDuration):
		case <-ctx.Done():
			r.mu.Lock()
			return ctx.Err()
		}
		r.mu.Lock()
		r.windowStart = time.Now()
		r.secondsUsed = 0
	}

	r.secondsUsed += secs
	return nil
}

// groqErrorMessage extrai uma mensagem legível de uma resposta de erro do Groq.
func groqErrorMessage(body []byte) string {
	var v struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &v); err == nil && v.Error.Message != "" {
		return v.Error.Message
	}
	return string(body)
}
