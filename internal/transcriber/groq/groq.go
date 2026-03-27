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

	"github.com/mguilhermetavares/odin-writer/internal/httpclient"
)

const (
	transcriptionURL     = "https://api.groq.com/openai/v1/audio/transcriptions"
	maxBytes             = 24 * 1024 * 1024 // 24MB — margem abaixo do limite de 25MB do Groq
	maxSecondsPerSegment = 19 * 60          // 1140s — limite por request
	maxSecondsPerHour    = 7200             // limite de segundos de áudio por hora do Groq (rate limiter)
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
			// Abortar em vez de continuar com lacuna: um artigo com trecho ausente
			// é pior do que nenhum artigo — o revisor não saberia o que perdeu.
			return "", fmt.Errorf("segmento %d/%d: %w", i+1, len(segments), err)
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

// ebmlIDWidth returns the byte width of an EBML element ID starting with b.
// EBML element IDs preserve their marker bit, so the width is determined by
// the position of the highest set bit. Returns 0 for invalid leading bytes.
func ebmlIDWidth(b byte) int {
	switch {
	case b >= 0x80:
		return 1
	case b >= 0x40:
		return 2
	case b >= 0x20:
		return 3
	case b >= 0x10:
		return 4
	default:
		return 0 // invalid (0x00–0x0F)
	}
}

// ebmlVINTSize parses an EBML variable-length integer used for element sizes.
// The leading marker bit is stripped from the value. Supports 1–8 byte VINTs,
// including the all-ones "unknown size" encoding used by streaming muxers.
// Returns (value, width, ok).
func ebmlVINTSize(data []byte, pos int) (val int64, width int, ok bool) {
	if pos >= len(data) {
		return 0, 0, false
	}
	b := data[pos]
	// Width = position of the highest set bit (1-indexed from MSB).
	width = 1
	for width <= 8 && (b>>(8-width))&1 == 0 {
		width++
	}
	if width > 8 || pos+width > len(data) {
		return 0, 0, false
	}
	// Strip marker bit and accumulate remaining bytes.
	val = int64(b & (0xFF >> width))
	for i := 1; i < width; i++ {
		val = val<<8 | int64(data[pos+i])
	}
	return val, width, true
}

// clusterTimecodeMs reads the Timecode element (ID 0xE7) from the Cluster
// starting at clusterOffset. limit bounds parsing to within a single cluster
// (pass the next cluster offset, or len(data) for the last cluster).
// Returns (milliseconds, true) on success, (0, false) on any parse error.
func clusterTimecodeMs(data []byte, clusterOffset, limit int) (ms int64, ok bool) {
	pos := clusterOffset

	// Skip the 4-byte Cluster ID.
	pos += 4
	if pos >= limit {
		return 0, false
	}

	// Skip the Cluster size VINT (value not needed; we iterate children instead).
	_, width, ok2 := ebmlVINTSize(data, pos)
	if !ok2 {
		return 0, false
	}
	pos += width

	// Iterate child elements looking for Timecode (ID 0xE7).
	const maxElements = 8
	for i := 0; i < maxElements && pos < limit && pos < len(data); i++ {
		idByte := data[pos]
		idWidth := ebmlIDWidth(idByte)
		if idWidth == 0 {
			return 0, false
		}
		if pos+idWidth > len(data) || pos+idWidth > limit {
			return 0, false
		}
		pos += idWidth

		elemSize, sizeWidth, ok3 := ebmlVINTSize(data, pos)
		if !ok3 {
			return 0, false
		}
		pos += sizeWidth

		if idByte == 0xE7 { // Timecode
			if elemSize > 8 || pos+int(elemSize) > len(data) {
				return 0, false
			}
			var val int64
			for j := 0; j < int(elemSize); j++ {
				val = val<<8 | int64(data[pos+j])
			}
			return val, true
		}

		// SimpleBlock (0xA3) and BlockGroup (0xA0) appear after Timecode per spec;
		// reaching them means Timecode was absent.
		if idByte == 0xA3 || idByte == 0xA0 {
			return 0, false
		}

		if pos+int(elemSize) > len(data) || pos+int(elemSize) > limit {
			return 0, false
		}
		pos += int(elemSize)
	}

	return 0, false
}

// splitWebm divide um ficheiro webm em chunks válidos sem ffmpeg.
//
// Cada chunk é cortado nos limites dos elementos Cluster do formato webm,
// respeitando dois limites:
//   - tamanho máximo: maxBytes (24MB) menos o tamanho do header
//   - duração máxima: maxSecondsPerSegment (1140s / 19min) por segmento
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

	// Duração por byte (fallback quando timecodes não estão disponíveis)
	var secsPerByte float64
	if totalDurationSec > 0 && totalSize > 0 {
		secsPerByte = float64(totalDurationSec) / float64(totalSize)
	}

	// Tentar extrair timecodes EBML de cada Cluster para estimativas mais precisas.
	clusterTimecodes := make([]float64, len(clusterOffsets))
	hasTimecodes := true
	for i, off := range clusterOffsets {
		limit := len(data)
		if i+1 < len(clusterOffsets) {
			limit = clusterOffsets[i+1]
		}
		ms, ok := clusterTimecodeMs(data, off, limit)
		if !ok {
			hasTimecodes = false
			break
		}
		clusterTimecodes[i] = float64(ms) / 1000.0
	}

	// Agrupar clusters em segmentos respeitando bodyBudget e maxSecondsPerHour
	var segments []segment
	segIdx := 0
	chunkBegin := clusterOffsets[0]
	chunkBeginIdx := 0
	boundaries := make([]int, len(clusterOffsets))
	copy(boundaries, clusterOffsets[1:])
	boundaries[len(boundaries)-1] = len(data)

	for i, nextBoundary := range boundaries {
		bodySize := nextBoundary - chunkBegin

		// Estimate segment duration using EBML timecodes when available,
		// falling back to byte proportion for the last boundary (EOF).
		var estimatedSecs float64
		nextClusterIdx := i + 1
		if hasTimecodes && nextClusterIdx < len(clusterTimecodes) {
			estimatedSecs = clusterTimecodes[nextClusterIdx] - clusterTimecodes[chunkBeginIdx]
		} else {
			estimatedSecs = float64(bodySize) * secsPerByte
		}

		sizeExceeded := bodySize >= bodyBudget
		// EBML timecodes are authoritative — split regardless of totalDurationSec.
		// Byte-proportion estimates are only meaningful when totalDurationSec > 0.
		timeExceeded := estimatedSecs >= maxSecondsPerSegment &&
			(hasTimecodes || totalDurationSec > 0)

		if sizeExceeded || timeExceeded {
			// Flush até ao cluster anterior quando possível.
			// Se boundaries[i-1] <= chunkBegin o cluster atual já foi o início do chunk
			// (acontece quando o limite é tão pequeno que cada cluster é flushed
			// individualmente) — nesse caso incluir o cluster corrente no segmento.
			var flushEnd, flushEndIdx int
			if i > 0 && boundaries[i-1] > chunkBegin {
				flushEnd = boundaries[i-1]
				flushEndIdx = i // boundaries[i-1] = clusterOffsets[i]
			} else {
				flushEnd = nextBoundary // cluster excede o limite sozinho — incluir na mesma
				flushEndIdx = i + 1
			}

			var segSecs float64
			if hasTimecodes && flushEndIdx < len(clusterTimecodes) {
				segSecs = clusterTimecodes[flushEndIdx] - clusterTimecodes[chunkBeginIdx]
			} else {
				segSecs = float64(flushEnd-chunkBegin) * secsPerByte
			}

			seg, err := writeSegment(data, header, chunkBegin, flushEnd, outDir, segIdx)
			if err != nil {
				return nil, err
			}
			seg.estimatedSecs = segSecs
			segments = append(segments, seg)
			segIdx++
			chunkBegin = flushEnd
			chunkBeginIdx = flushEndIdx
		}
	}

	// Último chunk (dados restantes) — always uses byte fallback (no next cluster boundary)
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

const maxRateLimitRetries = 3

// transcribeFile envia um único ficheiro de áudio à API Groq Whisper.
func (t *Transcriber) transcribeFile(ctx context.Context, audioPath string) (string, error) {
	return t.transcribeFileWithRetry(ctx, audioPath, 0)
}

func (t *Transcriber) transcribeFileWithRetry(ctx context.Context, audioPath string, attempt int) (string, error) {
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
		if attempt >= maxRateLimitRetries {
			return "", fmt.Errorf("groq rate limit after %d retries: %s", attempt, groqErrorMessage(body))
		}
		waitSecs := parseRetryAfter(resp, body)
		fmt.Printf("  rate limit atingido — aguardando %.0fs... (tentativa %d/%d)\n", waitSecs, attempt+1, maxRateLimitRetries)
		select {
		case <-time.After(time.Duration(waitSecs) * time.Second):
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return t.transcribeFileWithRetry(ctx, audioPath, attempt+1)
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
