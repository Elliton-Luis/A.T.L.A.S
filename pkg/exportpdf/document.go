// Package exportpdf renders printable schedules without an external runtime.
package exportpdf

import (
	"bytes"
	"fmt"
	"strings"
)

type Row struct{ Time, Activity, Prep, Leave string }
type Section struct {
	Title string
	Rows  []Row
	Notes []string
}
type Document struct {
	Title, Subtitle string
	Sections        []Section
}

// Render writes a paginated A4 calendar using embedded PDF base fonts.
func Render(document Document) ([]byte, error) {
	var pages []string
	var page strings.Builder
	y := 0.0
	text := func(x, y, size float64, bold bool, value string) {
		font := "F1"
		if bold {
			font = "F2"
		}
		fmt.Fprintf(&page, "BT /%s %.1f Tf 1 0 0 1 %.1f %.1f Tm (%s) Tj ET\n", font, size, x, y, pdfText(value))
	}
	newPage := func() {
		if page.Len() > 0 {
			pages = append(pages, page.String())
			page.Reset()
		}
		page.WriteString("0.10 0.16 0.24 rg\n")
		text(40, 800, 16, true, document.Title)
		text(40, 780, 9, false, document.Subtitle)
		text(40, 25, 8, false, fmt.Sprintf("ATLAS | Página %d", len(pages)+1))
		y = 751
	}
	heading := func(title string) {
		page.WriteString("0.90 0.94 0.98 rg\n")
		fmt.Fprintf(&page, "40 %.1f 515 22 re f\n", y-6)
		page.WriteString("0.10 0.16 0.24 rg\n")
		text(46, y, 10, true, title)
		y -= 28
	}
	columns := func() {
		text(44, y, 8, true, "Início/fim")
		text(110, y, 8, true, "Atividade")
		text(392, y, 8, true, "Preparo")
		text(475, y, 8, true, "Saída")
		y -= 16
	}
	newPage()
	for _, section := range document.Sections {
		if y < 115 {
			newPage()
		}
		heading(section.Title)
		if len(section.Rows) > 0 {
			columns()
		}
		for _, row := range section.Rows {
			cells := [][]string{wrap(row.Time, 12), wrap(row.Activity, 50), wrap(row.Prep, 14), wrap(row.Leave, 14)}
			lines := 1
			for _, cell := range cells {
				if len(cell) > lines {
					lines = len(cell)
				}
			}
			// Split exceptionally long activities across pages instead of clipping.
			for line := 0; line < lines; line++ {
				if y < 55 {
					newPage()
					heading(section.Title + " (continuação)")
					columns()
				}
				for col, x := range []float64{44, 110, 392, 475} {
					if line < len(cells[col]) {
						text(x, y, 9, false, cells[col][line])
					}
				}
				y -= 12
			}
			page.WriteString("0.82 0.86 0.90 RG\n")
			fmt.Fprintf(&page, "40 %.1f m 555 %.1f l S\n", y+4, y+4)
			y -= 5
		}
		for _, note := range section.Notes {
			for _, line := range wrap(note, 94) {
				if y < 55 {
					newPage()
					heading(section.Title + " (continuação)")
				}
				text(44, y, 9, false, line)
				y -= 13
			}
			y -= 4
		}
		y -= 10
	}
	pages = append(pages, page.String())
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "", "<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Courier-Bold /Encoding /WinAnsiEncoding >>"}
	var kids []string
	for _, content := range pages {
		pageID := len(objects) + 1
		streamID := pageID + 1
		kids = append(kids, fmt.Sprintf("%d 0 R", pageID))
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>", streamID), fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content))
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(pages), strings.Join(kids, " "))
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return out.Bytes(), nil
}

func wrap(text string, width int) []string {
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		var line []rune
		for _, word := range strings.Fields(paragraph) {
			runes := []rune(word)
			if len(line) > 0 && len(line)+len(runes)+1 > width {
				lines = append(lines, string(line))
				line = nil
			}
			for len(runes) > width {
				lines = append(lines, string(runes[:width]))
				runes = runes[width:]
			}
			if len(line) > 0 {
				line = append(line, ' ')
			}
			line = append(line, runes...)
		}
		if len(line) > 0 {
			lines = append(lines, string(line))
		}
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func pdfText(text string) string {
	var out strings.Builder
	for _, r := range text {
		switch r {
		case '(', ')', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\r', '\n', '\t':
			out.WriteByte(' ')
		case '—':
			out.WriteByte(0x97)
		case '–':
			out.WriteByte(0x96)
		case '’':
			out.WriteByte(0x92)
		case '“':
			out.WriteByte(0x93)
		case '”':
			out.WriteByte(0x94)
		case '•':
			out.WriteString("-")
		case '☐':
			out.WriteString("[ ]")
		case '☑':
			out.WriteString("[x]")
		default:
			if r >= 32 && r <= 255 {
				out.WriteByte(byte(r))
			} else {
				out.WriteByte('?')
			}
		}
	}
	return out.String()
}
