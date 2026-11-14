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
	"strings"
	"time"
	"unicode/utf8"
)

// RejectedError means Telegram explicitly rejected a message before delivery.
type RejectedError struct {
	Status      int
	Description string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("Telegram recusou envio (%d): %s", e.Status, e.Description)
}

type Client struct {
	botToken   string
	httpClient *http.Client
}

type Update struct {
	UpdateID int      `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	MessageID         int       `json:"message_id"`
	From              *User     `json:"from"`
	Chat              Chat      `json:"chat"`
	Date              int64     `json:"date"`
	Text              string    `json:"text"`
	Voice             *Voice    `json:"voice"`
	Audio             *Audio    `json:"audio"`
	Document          *Document `json:"document"`
	Caption           string    `json:"caption"`
	ForwardFrom       *User     `json:"forward_from"`
	ForwardSenderName string    `json:"forward_sender_name"`
	ForwardDate       int64     `json:"forward_date"`
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

type Audio struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	Performer    string `json:"performer"`
	Title        string `json:"title"`
	FileName     string `json:"file_name"`
	MimeType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
}

type Document struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name"`
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

// SendMessage sends a text message to the specified chat, splitting into chunks if text exceeds Telegram's limit.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	chunks := splitMessage(text, 4000)
	for i, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		if err := c.sendSingleMessage(ctx, chatID, chunk); err != nil {
			if i > 0 {
				return fmt.Errorf("entrega parcial: %v", err)
			}
			return err
		}
	}
	return nil
}

func (c *Client) sendSingleMessage(ctx context.Context, chatID int64, text string) error {
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
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return &RejectedError{Status: resp.StatusCode, Description: string(respBody)}
		}
		return fmt.Errorf("telegram sendMessage status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	remaining := text

	for len(remaining) > 0 {
		if len(remaining) <= maxLen {
			chunks = append(chunks, remaining)
			break
		}

		cut := maxLen
		sub := remaining[:maxLen]
		if idx := strings.LastIndex(sub, "\n"); idx >= maxLen-500 && idx > 0 {
			cut = idx + 1
		} else if idx := strings.LastIndexAny(sub, ".;"); idx >= maxLen-200 && idx > 0 {
			cut = idx + 1
		}
		for cut > 0 && !utf8.RuneStart(remaining[cut]) {
			cut--
		}
		if cut == 0 {
			_, cut = utf8.DecodeRuneInString(remaining)
		}

		chunks = append(chunks, strings.TrimRight(remaining[:cut], "\n"))
		remaining = strings.TrimLeft(remaining[cut:], "\n")
	}

	return chunks
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
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return &RejectedError{Status: resp.StatusCode, Description: string(body)}
		}
		return fmt.Errorf("telegram sendMessage status %d", resp.StatusCode)
	}
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

// DownloadFileToTemp downloads any Telegram file by file_id to a secure local temp file matching the pattern.
func (c *Client) DownloadFileToTemp(ctx context.Context, fileID, pattern string) (string, error) {
	if pattern == "" {
		pattern = "atlas_download_*"
	}

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

	// Preserve extension if pattern doesn't specify one
	ext := filepath.Ext(fileResp.Result.FilePath)
	if !strings.Contains(pattern, "*") {
		pattern += "_*" + ext
	} else if !strings.HasSuffix(pattern, ext) && ext != "" {
		pattern += ext
	}

	tmpFile, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, respDL.Body); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("save temp file: %w", err)
	}

	return tmpFile.Name(), nil
}

// DownloadVoiceFile downloads an audio file by file_id to a secure local temp file.
func (c *Client) DownloadVoiceFile(ctx context.Context, fileID string) (string, error) {
	return c.DownloadFileToTemp(ctx, fileID, "atlas_voice_*.ogg")
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

// SendChatAction informs the user that the bot is processing or typing (e.g. "typing", "upload_document").
func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	if action == "" {
		action = "typing"
	}

	payload := map[string]interface{}{
		"chat_id": chatID,
		"action":  action,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendChatAction", c.botToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}
