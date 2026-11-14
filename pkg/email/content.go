package email

import (
	"encoding/base64"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
)

var htmlNoise = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(?:script|style)>`)
var htmlTags = regexp.MustCompile(`(?s)<[^>]+>`)

func readMessageContent(raw []byte, summary *MessageSummary) {
	message, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return
	}
	summary.Subject = decodeHeader(message.Header.Get("Subject"))
	summary.From = decodeHeader(message.Header.Get("From"))
	summary.Date = message.Header.Get("Date")
	content := mimeText(message.Header.Get("Content-Type"), message.Header.Get("Content-Transfer-Encoding"), message.Body, 0)
	runes := []rune(strings.TrimSpace(content))
	if len(runes) > 2500 {
		runes = runes[:2500]
	}
	summary.Snippet = string(runes)
}

func mimeText(contentType, encoding string, reader io.Reader, depth int) string {
	if depth > 4 {
		return ""
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		mediaType = "text/plain"
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		parts := multipart.NewReader(reader, params["boundary"])
		var fallback string
		for i := 0; i < 20; i++ {
			part, err := parts.NextPart()
			if err != nil {
				break
			}
			if strings.HasPrefix(part.Header.Get("Content-Disposition"), "attachment") {
				part.Close()
				continue
			}
			text := mimeText(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), part, depth+1)
			part.Close()
			if strings.HasPrefix(part.Header.Get("Content-Type"), "text/plain") && text != "" {
				return text
			}
			if fallback == "" {
				fallback = text
			}
		}
		return fallback
	}
	if mediaType != "text/plain" && mediaType != "text/html" {
		return ""
	}
	switch strings.ToLower(encoding) {
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, reader)
	case "quoted-printable":
		reader = quotedprintable.NewReader(reader)
	}
	data, _ := io.ReadAll(io.LimitReader(reader, 16384))
	text := string(data)
	if mediaType == "text/html" {
		text = htmlNoise.ReplaceAllString(text, "")
		text = html.UnescapeString(htmlTags.ReplaceAllString(text, " "))
		text = strings.Join(strings.Fields(text), " ")
	}
	return text
}
