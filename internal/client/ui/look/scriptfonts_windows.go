//go:build windows

package look

import (
	"os"
	"path/filepath"

	"gioui.org/font"
	"gioui.org/font/opentype"
)

// scriptFonts loads Windows' own fonts for the scripts Ryotunes' faces lack
// and Gio's system font lookup misses: Nirmala UI (Tamil, Hindi, Telugu,
// Malayalam and the other Indian scripts) and Malgun Gothic (Korean). Without
// them those lyrics and titles showed as boxes.
func scriptFonts() []font.FontFace {
	dir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	if os.Getenv("WINDIR") == "" {
		dir = `C:\Windows\Fonts`
	}
	var faces []font.FontFace
	for _, name := range []string{"Nirmala.ttc", "malgun.ttf", "malgunbd.ttf"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue // not on this edition of Windows
		}
		collection, err := opentype.ParseCollection(data)
		if err != nil {
			continue
		}
		faces = append(faces, collection...)
	}
	return faces
}
