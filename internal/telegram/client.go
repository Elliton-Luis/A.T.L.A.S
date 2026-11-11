package telegram

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
	"time"
)

type Client struct {
	botToken   string
	httpClient *http.Client
}

type Update struct {
	UpdateID int      `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	MessageID         int    `json:"message_id"`
	From              *User  `json:"from"`
	Chat              Chat   `json:"chat"`
	Date              int64  `json:"date"`
	Text              string `json:"text"`
	Voice             *Voice `json:"voice"`
	Caption           string `json:"caption"`
	ForwardFrom       *User  `json:"forward_from"`
	ForwardSenderName string `json:"forward_sender_name"`
	ForwardDate       int64  `json:"forward_date"`
}

type InlineKeyboardButton struct {
	Text   string  `json:"text"`
	URL    string  `json:"url,omitempty"`
	WebApp *WebApp `json:"web_app,omitempty"`
}

type WebApp struct {
	URL string `json:"url"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Username string `json:"username"`
}

type Voice struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	MimeType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
}

type FileResponse struct {
	OK     bool `json:"ok"`
	Result struct {
		FileID   string `json:"file_id"`
		FilePath string `json:"file_path"`
	} `json:"result"`
}

func NewClient(botToken string) *Client {
	return &Client{
		botToken: botToken,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

// SendMessage sends a text message to the specified chat.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.botToken)

	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal sendMessage: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do sendMessage: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		// If Markdown parsing fails, retry as plain text for resilience
		if resp.StatusCode == http.StatusBadRequest && bytes.Contains(respBody, []byte("can't parse entities")) {
			return c.sendPlainMessage(ctx, chatID, text)
		}
		return fmt.Errorf("telegram sendMessage status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (c *Client) sendPlainMessage(ctx context.Context, chatID int64, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.botToken)
	payload := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// SendMessageWithWebApp sends a message containing an inline button to launch the WebApp HUD.
func (c *Client) SendMessageWithWebApp(ctx context.Context, chatID int64, text, buttonText, webAppURL string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.botToken)

	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{
					Text:   buttonText,
					WebApp: &WebApp{URL: webAppURL},
				},
			},
		},
	}

	payload := map[string]interface{}{
		"chat_id":      chatID,
		"text":         text,
		"parse_mode":   "Markdown",
		"reply_markup": markup,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal sendMessageWithWebApp: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do sendMessageWithWebApp: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram sendMessageWithWebApp status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// DownloadVoiceFile downloads an audio file by file_id to a secure local temp file.
func (c *Client) DownloadVoiceFile(ctx context.Context, fileID string) (string, error) {
	// 1. Get file path
	infoURL := fmt.Sprintf("https://api.telegram.org/bot%s/getFile?file_id=%s", c.botToken, fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, infoURL, nil)
	if err != nil {
		return "", fmt.Errorf("getFile req: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("getFile do: %w", err)
	}
	defer resp.Body.Close()

	var fileResp FileResponse
	if err := json.NewDecoder(resp.Body).Decode(&fileResp); err != nil {
		return "", fmt.Errorf("decode getFile: %w", err)
	}

	if !fileResp.OK || fileResp.Result.FilePath == "" {
		return "", fmt.Errorf("telegram getFile returned error or empty path")
	}

	// 2. Download the binary
	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", c.botToken, fileResp.Result.FilePath)
	reqDL, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("download req: %w", err)
	}

	respDL, err := c.httpClient.Do(reqDL)
	if err != nil {
		return "", fmt.Errorf("download do: %w", err)
	}
	defer respDL.Body.Close()

	// Write to secure temporary file (0600)
	tmpFile, err := os.CreateTemp("", "atlas_voice_*.ogg")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, respDL.Body); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("save voice temp file: %w", err)
	}

	return tmpFile.Name(), nil
}

// SendDocument uploads a file (e.g. database backup) to Telegram.
func (c *Client) SendDocument(ctx context.Context, chatID int64, filePath, caption string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file for sendDocument: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("document", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("copy form file: %w", err)
	}

	_ = writer.WriteField("chat_id", fmt.Sprintf("%d", chatID))
	if caption != "" {
		_ = writer.WriteField("caption", caption)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", c.botToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("create sendDocument req: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do sendDocument: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sendDocument failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
