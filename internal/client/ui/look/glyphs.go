package look

import (
	"strings"
	"unicode"

	"gioui.org/font"
	"gioui.org/text"
	"golang.org/x/image/math/fixed"
)

// Undrawable reports lyrics this computer has no font for: more than a fifth
// of their letters shape to the missing-glyph box. A wall of boxes is worse
// than saying there are no lyrics.
func Undrawable(shaper *text.Shaper, face font.Font, lines []string) bool {
	if shaper == nil || len(lines) == 0 {
		return false
	}
	sample := strings.Join(lines[:min(len(lines), 40)], " ")
	if strings.IndexFunc(sample, func(r rune) bool { return !unicode.IsSpace(r) }) < 0 {
		return false
	}
	shaper.LayoutString(text.Parameters{Font: face, PxPerEm: fixed.I(16), MaxWidth: 1 << 24}, sample)
	total, missing := 0, 0
	for glyph, ok := shaper.NextGlyph(); ok; glyph, ok = shaper.NextGlyph() {
		if glyph.Runes == 0 || glyph.Flags&text.FlagLineBreak != 0 {
			continue
		}
		total++
		// The low 16 bits are the glyph in its font (the rest names the
		// font); glyph 0 is the font's missing-glyph box.
		if uint16(glyph.ID) == 0 {
			missing++
		}
	}
	return total > 0 && missing*5 > total
}
