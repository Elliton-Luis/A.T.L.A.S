package schedule

import "testing"

func TestEventDurationsUseMaximumAndDoNotConfuseStartOrPrep(t *testing.T) {
	for _, test := range []struct {
		text, clock string
		reply       bool
		minutes     int
		found       bool
	}{
		{"2-3 horas", "", true, 180, true}, {"entre 2 e 3 horas", "", true, 180, true}, {"2h-3h", "", true, 180, true}, {"de 30 minutos a 1 hora", "", true, 60, true}, {"1,5 horas", "", true, 90, true},
		{"Marque aula às 18h", "18:00", false, 0, false}, {"Marque aula às 18h por 2-3 horas", "18:00", false, 180, true},
		{"Aula das 23h às 2h", "23:00", false, 180, true}, {"Aula das 14:30 às 16:00", "14:30", false, 90, true},
		{"Marque dentista daqui 30 minutos", "", false, 0, false}, {"Aula por volta das 18h", "18:00", false, 0, false},
		{"Aula às 18h, duração 2 horas, preparo 30 minutos", "18:00", false, 120, true},
		{"Aula às 18h por 2 horas e 30 minutos de preparo", "18:00", false, 120, true},
	} {
		minutes, found, err := ExtractDuration(test.text, test.clock, test.reply)
		if err != nil || minutes != test.minutes || found != test.found {
			t.Errorf("%s: %d %t (%v)", test.text, minutes, found, err)
		}
	}
	if _, _, err := ExtractDuration("das 8h às 8h", "08:00", false); err == nil {
		t.Fatal("intervalo ambíguo aceito")
	}
}

func TestCompactIntervalsRemainRangesNotSums(t *testing.T) {
	for _, test := range []struct {
		text  string
		want  int
		reply bool
	}{{"1h30-2h", 120, true}, {"1,5h-2,5h", 150, true}, {"das23hàs2h", 180, false}} {
		minutes, found, err := ExtractDuration(test.text, "23:00", test.reply)
		if err != nil || !found || minutes != test.want {
			t.Errorf("%s: %d %t (%v)", test.text, minutes, found, err)
		}
	}
}
