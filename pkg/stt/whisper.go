package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"atlas/pkg/pool"
)

type Client struct {
	pool       *pool.KeyPool
	httpClient *http.Client
}

type TranscribeResponse struct {
	Text string `json:"text"`
}

func NewClient(rawKeys string) *Client {
	return &Client{
		pool: pool.New(rawKeys),
		httpClient: &http.Client{
			Timeout: 25 * time.Second,
		},
	}
}

// Transcribe sends the audio file to Groq Whisper and returns the transcribed text.
// Automatically rotates and retries across the key pool if rate limits (429) occur.
func (c *Client) Transcribe(ctx context.Context, audioFilePath string) (string, error) {
	if c.pool.IsEmpty() {
		return "", fmt.Errorf("GROQ_API_KEY is not configured")
	}

	fileBytes, err := os.ReadFile(audioFilePath)
	if err != nil {
		return "", fmt.Errorf("read audio file: %w", err)
	}

	keys := c.pool.OrderedKeys()
	var lastErr error

	for _, key := range keys {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		part, err := writer.CreateFormFile("file", "voice.ogg")
		if err != nil {
			return "", fmt.Errorf("create form file: %w", err)
		}
		if _, err := io.Copy(part, bytes.NewReader(fileBytes)); err != nil {
			return "", fmt.Errorf("copy audio to multipart: %w", err)
		}

		_ = writer.WriteField("model", "whisper-large-v3")
		_ = writer.WriteField("language", "pt")
		_ = writer.WriteField("response_format", "json")

		if err := writer.Close(); err != nil {
			return "", fmt.Errorf("close writer: %w", err)
		}

		url := "https://api.groq.com/openai/v1/audio/transcriptions"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
		if err != nil {
			return "", fmt.Errorf("create whisper req: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("User-Agent", "AtlasBot/1.0")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("do whisper req: %w", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("groq whisper status %d: %s", resp.StatusCode, string(respBody))
			// Rate limit or server error: retry with next key in pool
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode >= 500 {
				continue
			}
			return "", lastErr
		}

		var res TranscribeResponse
		err = json.NewDecoder(resp.Body).Decode(&res)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("decode whisper response: %w", err)
			continue
		}

		return strings.TrimSpace(res.Text), nil
	}

	return "", fmt.Errorf("groq whisper all keys failed: %w", lastErr)
}
