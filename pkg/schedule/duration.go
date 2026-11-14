package schedule

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"atlas/pkg/duration"
	"atlas/pkg/synonyms"
)

var timeWindow = regexp.MustCompile(`\bdas?\s*([0-9]{1,2})(?:(?::|h)([0-9]{2})?)?(?:\s*horas?)?\s*(?:as|ate)\s*([0-9]{1,2})(?:(?::|h)([0-9]{2})?)?(?:\s*horas?)?\b`)
var durationMarker = regexp.MustCompile(`\b(?:duracao|dura|durar|durante|por|leva)\b`)
var startClock = regexp.MustCompile(`\b(?:as|a partir das)\s+[0-9]{1,2}(?:(?::|h)[0-9]{0,2})?(?:\s*horas?)?\b`)
var relativeStart = regexp.MustCompile(`\b(?:daqui(?: a)?|em)\s+[0-9]+\s*(?:min(?:utos?)?|horas?|h)\b`)
var otherField = regexp.MustCompile(`;|,\s*[^0-9\s]|\b(?:preparo|deslocamento)\b|(?:\be\s+)?(?:\bcom\s+)?[0-9]+\s*(?:minutos?|mins?|min|h|horas?)\s+(?:de\s+preparo|para\s+(?:me\s+)?arrumar|pra\s+(?:me\s+)?arrumar|de\s+deslocamento|para\s+chegar)`)

// ExtractDuration distinguishes a start clock from a duration. Replies to a
// duration question may contain just "2-3 horas"; ranges take their maximum.
func ExtractDuration(text, clock string, reply bool) (int, bool, error) {
	clean := synonyms.RemoveAccents(strings.ToLower(text))
	for _, phrase := range []string{"por volta das", "por volta de"} {
		clean = strings.ReplaceAll(clean, phrase, "as")
	}
	clean = strings.ReplaceAll(clean, "por favor", "")
	if reply {
		minutes, _, found, err := duration.Parse(clean)
		return minutes, found, err
	}
	if match := timeWindow.FindStringSubmatch(clean); len(match) > 0 {
		startH, _ := strconv.Atoi(match[1])
		startM, _ := strconv.Atoi(match[2])
		endH, _ := strconv.Atoi(match[3])
		endM, _ := strconv.Atoi(match[4])
		if startH > 23 || endH > 23 || startM > 59 || endM > 59 {
			return 0, true, fmt.Errorf("horário inválido no intervalo")
		}
		minutes := (endH*60 + endM) - (startH*60 + startM)
		if minutes == 0 {
			return 0, true, fmt.Errorf("início e fim iguais: informe a duração explicitamente")
		}
		if minutes < 0 {
			minutes += 1440
		}
		return minutes, true, nil
	}
	if marker := durationMarker.FindStringIndex(clean); marker != nil {
		clean = clean[marker[1]:]
		if stop := otherField.FindStringIndex(clean); stop != nil {
			clean = clean[:stop[0]]
		}
	} else {
		if strings.Contains(clean, "preparo") || strings.Contains(clean, "deslocamento") || strings.Contains(clean, "arrumar") {
			return 0, false, nil
		}
		clean = startClock.ReplaceAllString(clean, " ")
		clean = relativeStart.ReplaceAllString(clean, " ")
		if parsed, err := strconv.Atoi(strings.SplitN(clock, ":", 2)[0]); err == nil {
			pattern := regexp.MustCompile(fmt.Sprintf(`\b0?%d(?:h(?:[0-9]{2})?|:[0-9]{2})\b`, parsed))
			clean = pattern.ReplaceAllString(clean, " ")
		}
	}
	minutes, _, found, err := duration.Parse(clean)
	return minutes, found, err
}
