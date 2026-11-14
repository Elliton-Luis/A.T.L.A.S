package planner

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var compactHours = regexp.MustCompile(`(?i)\b([0-9]{1,2})h([0-9]{1,2})(?:min)?\b`)
var unitDuration = regexp.MustCompile(`(?i)\b([0-9]{1,4})\s*(horas?|hrs?|h|minutos?|mins?|min|m)\b`)
var writtenDuration = regexp.MustCompile(`(?i)\b((?:vinte|trinta|quarenta|cinquenta)(?:\s+e\s+(?:um|uma|dois|duas|tres|três|quatro|cinco|seis|sete|oito|nove))?|um|uma|dois|duas|tres|três|quatro|cinco|seis|sete|oito|nove|dez|onze|doze|treze|catorze|quatorze|quinze|dezesseis|dezessete|dezoito|dezenove|sessenta)\s+(horas?|minutos?)\b`)
var decimalDuration = regexp.MustCompile(`\b[0-9]+[.,][0-9]+\s*(?:h|horas?|min(?:utos?)?)\b`)
var durationPhrases = []struct {
	pattern *regexp.Regexp
	minutes int
}{
	{regexp.MustCompile(`(?i)\bmeia hora\b`), 30},
	{regexp.MustCompile(`(?i)\bum quarto de hora\b`), 15},
}

// ParseDuration returns the remaining text so time never becomes part of the goal.
func ParseDuration(text string) (minutes int, rest string, found bool, err error) {
	rest = text
	if decimalDuration.MatchString(text) {
		return 0, text, true, fmt.Errorf("informe minutos inteiros ou uma duração como 1h30")
	}
	count := 0
	rest = compactHours.ReplaceAllStringFunc(rest, func(part string) string {
		m := compactHours.FindStringSubmatch(part)
		h, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		if min >= 60 {
			err = fmt.Errorf("minutos após h devem estar entre 00 e 59")
		}
		minutes += h*60 + min
		count++
		return " "
	})
	for _, phrase := range durationPhrases {
		if phrase.pattern.MatchString(rest) {
			minutes += phrase.minutes
			count++
			rest = phrase.pattern.ReplaceAllString(rest, " ")
		}
	}
	words := map[string]int{"um": 1, "uma": 1, "dois": 2, "duas": 2, "tres": 3, "três": 3, "quatro": 4, "cinco": 5, "seis": 6, "sete": 7, "oito": 8, "nove": 9, "dez": 10, "onze": 11, "doze": 12, "treze": 13, "catorze": 14, "quatorze": 14, "quinze": 15, "dezesseis": 16, "dezessete": 17, "dezoito": 18, "dezenove": 19, "vinte": 20, "trinta": 30, "quarenta": 40, "cinquenta": 50, "sessenta": 60}
	rest = writtenDuration.ReplaceAllStringFunc(rest, func(part string) string {
		m := writtenDuration.FindStringSubmatch(strings.ToLower(part))
		value := 0
		for _, word := range strings.Fields(m[1]) {
			value += words[word]
		}
		if strings.HasPrefix(m[2], "hora") {
			value *= 60
		}
		minutes += value
		count++
		return " "
	})
	rest = unitDuration.ReplaceAllStringFunc(rest, func(part string) string {
		m := unitDuration.FindStringSubmatch(part)
		value, _ := strconv.Atoi(m[1])
		if strings.HasPrefix(strings.ToLower(m[2]), "h") {
			value *= 60
		}
		minutes += value
		count++
		return " "
	})
	found = count > 0
	if found && (minutes < 1 || minutes > 480) {
		err = fmt.Errorf("escolha um bloco entre 1 e 480 minutos")
	}
	if count > 1 && strings.Contains(strings.ToLower(text), " ou ") {
		err = fmt.Errorf("escolha uma única duração para essa atividade")
	}
	return minutes, strings.Join(strings.Fields(rest), " "), found, err
}
