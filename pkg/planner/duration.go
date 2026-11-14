package planner

import (
	"atlas/pkg/duration"
	"fmt"
)

func ParseDuration(text string) (int, string, bool, error) {
	minutes, rest, found, err := duration.Parse(text)
	if err == nil && found && (minutes < 1 || minutes > 480) {
		err = fmt.Errorf("escolha um bloco entre 1 e 480 minutos")
	}
	return minutes, rest, found, err
}
