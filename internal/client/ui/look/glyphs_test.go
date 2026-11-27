package look

import (
	"testing"

	"gioui.org/font"
	"gioui.org/text"

	"github.com/ryoku-dev/ryoku/docs/lithic/library/go/fonts"
	"github.com/ryoku-dev/ryotunes/internal/model"
)

const tamilLine = "நீ இல்லாமல் நான் இல்லை"

// Without a font for Tamil the lyrics would be boxes: they count as
// undrawable. English always draws with the app's own fonts.
func TestUndrawableWithoutAFontForTheScript(t *testing.T) {
	ownFontsOnly := text.NewShaper(text.NoSystemFonts(), text.WithCollection(fonts.Collection()))
	if !Undrawable(ownFontsOnly, font.Font{}, []string{tamilLine, tamilLine}) {
		t.Error("Tamil drew with no Tamil font")
	}
	if Undrawable(ownFontsOnly, font.Font{}, []string{"When the sun dies", "Till the day shines"}) {
		t.Error("English counted as undrawable")
	}
}

// A timed piece that starts with a vowel sign joins the piece before it.
func TestKaraokeJoinsASplitVowelSign(t *testing.T) {
	at := func(v uint64) *uint64 { return &v }
	line := model.LyricsLine{
		Text: "நீ வா", TimeMS: at(1000), EndTimeMS: at(3000),
		Words: []model.LyricsWord{
			{Text: "ந", StartMS: 1000, EndMS: 1400},
			{Text: "ீ", StartMS: 1400, EndMS: 1800},
			{Text: " வா", StartMS: 1800, EndMS: 2600},
		},
	}
	words := PrepareKaraoke([]model.LyricsLine{line})
	if len(words) != 1 || len(words[0]) != 2 || words[0][0].Text != "நீ" || words[0][1].Text != "வா" {
		t.Fatalf("pieces = %+v, want நீ and வா", words)
	}
}

// TestSystemFontsDrawTamil reports whether this PC's fonts draw Tamil (with
// the shaper the app uses). RYOTUNES_FONT_TESTS=1.
func TestSystemFontsDrawTamil(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	withSystem := text.NewShaper(text.WithCollection(fonts.Collection()))
	t.Logf("Tamil undrawable with system fonts: %v", Undrawable(withSystem, font.Font{}, []string{tamilLine}))
}
