package email

import (
	"regexp"
	"strings"
	"time"

	"atlas/pkg/synonyms"
)

type Query struct {
	From    string
	Subject string
	Since   time.Time
	Before  time.Time
	Unread  bool
	UID     string
}

var fromQuery = regexp.MustCompile(`(?i)\b(?:remetente|from)\s*:?\s*(.+?)(?:\s+(?:desde|hoje|ontem|assunto|nao lidos|não lidos)\b|$)`)
var naturalFromQuery = regexp.MustCompile(`(?i)\b(?:emails?|e-mails?|correio)\s+(?:do|da|de)\s+(.+?)(?:\s+(?:de ontem|de hoje|desde|hoje|ontem|com assunto|assunto)\b|$)`)
var subjectQuery = regexp.MustCompile(`(?i)\bassunto\s*:?\s*(.+?)(?:\s+(?:desde|hoje|ontem)\b|$)`)
var sinceQuery = regexp.MustCompile(`\bdesde\s+(\d{4}-\d{2}-\d{2})\b`)
var uidQuery = regexp.MustCompile(`(?i)\buid\s*:?\s*([0-9]+)\b`)

func HasQueryFilters(text string) bool {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	return strings.Contains(clean, "ontem") || strings.Contains(clean, "hoje") || strings.Contains(clean, "desde") || strings.Contains(clean, "assunto") || strings.Contains(clean, "remetente") || strings.Contains(clean, "from:") || naturalFromQuery.MatchString(clean) || uidQuery.MatchString(clean)
}

func ParseQuery(text string, now time.Time) Query {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	q := Query{Unread: true}
	for _, pattern := range []*regexp.Regexp{fromQuery, naturalFromQuery} {
		if m := pattern.FindStringSubmatch(text); len(m) > 1 {
			q.From = strings.Trim(m[1], " \"'.?!")
			break
		}
	}
	if m := subjectQuery.FindStringSubmatch(text); len(m) > 1 {
		q.Subject = strings.Trim(m[1], " \"'.?!")
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if strings.Contains(clean, "ontem") {
		q.Since = today.AddDate(0, 0, -1)
		q.Before = today
	} else if strings.Contains(clean, "hoje") {
		q.Since = today
		q.Before = today.AddDate(0, 0, 1)
	}
	if strings.Contains(clean, "desde ontem") {
		q.Before = time.Time{}
	}
	if m := sinceQuery.FindStringSubmatch(clean); len(m) > 1 {
		q.Since, _ = time.ParseInLocation("2006-01-02", m[1], now.Location())
		q.Before = time.Time{}
	}
	if m := uidQuery.FindStringSubmatch(clean); len(m) > 1 {
		q.UID = m[1]
	}
	if HasQueryFilters(text) || strings.Contains(clean, "recentes") || strings.Contains(clean, "ultimos") {
		q.Unread = false
	}
	if strings.Contains(clean, "nao lidos") || strings.Contains(clean, "nao lidas") {
		q.Unread = true
	}
	return q
}

func (q Query) searchCriteria() string {
	var terms []string
	if q.Unread {
		terms = append(terms, "UNSEEN")
	}
	if q.From != "" {
		terms = append(terms, "FROM "+escapeIMAPString(q.From))
	}
	if q.Subject != "" {
		terms = append(terms, "SUBJECT "+escapeIMAPString(q.Subject))
	}
	if !q.Since.IsZero() {
		terms = append(terms, "SINCE "+q.Since.Format("02-Jan-2006"))
	}
	if !q.Before.IsZero() {
		terms = append(terms, "BEFORE "+q.Before.Format("02-Jan-2006"))
	}
	if q.UID != "" {
		terms = append(terms, "UID "+q.UID)
	}
	if len(terms) == 0 {
		return "ALL"
	}
	return strings.Join(terms, " ")
}
