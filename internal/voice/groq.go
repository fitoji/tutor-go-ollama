package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Transcriber sends the audio to an OpenAI-compatible transcription endpoint.
// Groq's whisper-large-v3-turbo is the default: no local model, ~1-2 s for a
// sentence, and multilingual training that tolerates an accent better than an
// English-only model.
type GroqTranscriber struct {
	Base  string // https://api.groq.com/openai/v1
	Key   string
	Model string // whisper-large-v3-turbo
	HTTP  *http.Client
}

func (g GroqTranscriber) base() string {
	if g.Base == "" {
		return "https://api.groq.com/openai/v1"
	}
	return strings.TrimRight(g.Base, "/")
}

func (g GroqTranscriber) model() string {
	if g.Model == "" {
		return "whisper-large-v3-turbo"
	}
	return g.Model
}

func (g GroqTranscriber) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// Transcribe uploads the audio as a WAV and returns the text. lang is an
// ISO-639-1 code ("en"); empty lets the model guess.
func (g GroqTranscriber) Transcribe(ctx context.Context, pcm []byte, lang string) (string, error) {
	if len(pcm) == 0 {
		return "", nil
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("model", g.model())
	_ = w.WriteField("response_format", "json")
	_ = w.WriteField("temperature", "0")
	if lang != "" {
		_ = w.WriteField("language", lang)
	}
	fw, err := w.CreateFormFile("file", "speech.wav")
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(WAV(pcm)); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.base()+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+g.Key)

	resp, err := g.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("no pude transcribir: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", transcribeError(resp)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("no pude leer la transcripción: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}

// transcribeError explains the status codes a learner can actually fix.
func transcribeError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	detail := strings.TrimSpace(string(body))
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("la clave de transcripción no sirve (%d): revisá GROQ_API_KEY", resp.StatusCode)
	case http.StatusTooManyRequests:
		return fmt.Errorf("el servicio de transcripción está limitado ahora (429): probá en un rato")
	case http.StatusRequestEntityTooLarge:
		return fmt.Errorf("el audio es demasiado largo para transcribir (413)")
	}
	if detail == "" {
		return fmt.Errorf("la transcripción falló con %d", resp.StatusCode)
	}
	return fmt.Errorf("la transcripción falló con %d: %s", resp.StatusCode, detail)
}
