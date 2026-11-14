// Package duration interprets explicit time budgets, taking the upper bound of ranges.
package duration

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var compactHours = regexp.MustCompile(`(?i)\b([0-9]{1,2})h([0-9]{1,2})(?:min)?\b`)
var unitDuration = regexp.MustCompile(`(?i)\b([0-9]{1,6})\s*(dias?|d|horas?|hrs?|h|minutos?|mins?|min|m)\b`)
var writtenDuration = regexp.MustCompile(`(?i)\b((?:vinte|trinta|quarenta|cinquenta)(?:\s+e\s+(?:um|uma|dois|duas|tres|três|quatro|cinco|seis|sete|oito|nove))?|um|uma|dois|duas|tres|três|quatro|cinco|seis|sete|oito|nove|dez|onze|doze|treze|catorze|quatorze|quinze|dezesseis|dezessete|dezoito|dezenove|sessenta)\s+(dias?|horas?|minutos?)\b`)
var decimalDuration = regexp.MustCompile(`(?i)\b([0-9]+)[.,]([0-9]{1,2})\s*(dias?|d|horas?|h|min(?:utos?)?)\b`)
var durationPhrases = []struct {
	pattern *regexp.Regexp
	minutes int
}{
	{regexp.MustCompile(`(?i)\bmeia hora\b`), 30},
	{regexp.MustCompile(`(?i)\bum quarto de hora\b`), 15},
}

var sharedRange = regexp.MustCompile(`(?i)\b(?:de\s+)?([0-9]+)\s*(?:-|a|até|ate)\s*([0-9]+)\s*(dias?|d|horas?|hrs?|h|minutos?|mins?|min|m)\b`)
var betweenRange = regexp.MustCompile(`(?i)\bentre\s+([0-9]+)\s+e\s+([0-9]+)\s*(dias?|d|horas?|hrs?|h|minutos?|mins?|min|m)\b`)
var mixedRange = regexp.MustCompile(`(?i)\b([0-9]+)\s*(dias?|d|horas?|hrs?|h|minutos?|mins?|min|m)\s*(?:-|a|até|ate)\s*([0-9]+)\s*(dias?|d|horas?|hrs?|h|minutos?|mins?|min|m)\b`)
var alternatives = regexp.MustCompile(`(?i)\s+ou\s+`)
var negativeDuration = regexp.MustCompile(`(?:^|\s)-[0-9]`)

func Parse(text string) (int, string, bool, error) {
	if negativeDuration.MatchString(text) {
		return 0, text, true, fmt.Errorf("a duração precisa ser positiva")
	}
	if alternatives.MatchString(text) {
		maximum, count := 0, 0
		var rest []string
		for _, part := range alternatives.Split(text, -1) {
			minutes, left, found, err := Parse(part)
			if err != nil {
				return 0, text, found, err
			}
			if found {
				count++
				if minutes > maximum {
					maximum = minutes
				}
			}
			rest = append(rest, left)
		}
		if count > 1 {
			return maximum, strings.TrimSpace(strings.Join(rest, " ")), true, nil
		}
	}
	return parseSingle(text)
}

func parseSingle(text string) (minutes int, rest string, found bool, err error) {
	rest = text
	count := 0
	rest = compactHours.ReplaceAllStringFunc(rest, func(part string) string {
		m := compactHours.FindStringSubmatch(part)
		h, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		if min >= 60 {
			err = fmt.Errorf("minutos após h devem estar entre 00 e 59")
		}
		return strconv.Itoa(h*60+min) + " min"
	})
	rest = decimalDuration.ReplaceAllStringFunc(rest, func(part string) string {
		m := decimalDuration.FindStringSubmatch(part)
		whole, _ := strconv.Atoi(m[1])
		fraction, _ := strconv.Atoi(m[2])
		denominator := 10
		if len(m[2]) == 2 {
			denominator = 100
		}
		scale := 1
		if strings.HasPrefix(strings.ToLower(m[3]), "h") {
			scale = 60
		}
		if strings.HasPrefix(strings.ToLower(m[3]), "d") {
			scale = 1440
		}
		if fraction*scale%denominator != 0 {
			err = fmt.Errorf("a duração precisa resultar em minutos inteiros")
		}
		return strconv.Itoa(whole*scale+fraction*scale/denominator) + " min"
	})
	rest = mixedRange.ReplaceAllStringFunc(rest, func(part string) string {
		m := mixedRange.FindStringSubmatch(part)
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[3])
		scale := func(unit string) int {
			if strings.HasPrefix(strings.ToLower(unit), "h") {
				return 60
			}
			if strings.HasPrefix(strings.ToLower(unit), "d") {
				return 1440
			}
			return 1
		}
		a *= scale(m[2])
		b *= scale(m[4])
		if a > b {
			b = a
		}
		return strconv.Itoa(b) + " min"
	})
	for _, pattern := range []*regexp.Regexp{sharedRange, betweenRange} {
		rest = pattern.ReplaceAllStringFunc(rest, func(part string) string {
			match := pattern.FindStringSubmatch(part)
			a, _ := strconv.Atoi(match[1])
			b, _ := strconv.Atoi(match[2])
			if a > b {
				b = a
			}
			return strconv.Itoa(b) + " " + match[3]
		})
	}
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
		if strings.HasPrefix(m[2], "dia") {
			value *= 1440
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
		if strings.HasPrefix(strings.ToLower(m[2]), "d") {
			value *= 1440
		}
		minutes += value
		count++
		return " "
	})
	found = count > 0
	if found && (minutes < 1 || minutes > 366*1440) {
		err = fmt.Errorf("informe duração positiva de até 366 dias")
	}
	return minutes, strings.Join(strings.Fields(rest), " "), found, err
}
