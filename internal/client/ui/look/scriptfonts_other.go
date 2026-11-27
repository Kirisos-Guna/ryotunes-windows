//go:build !windows

package look

import "gioui.org/font"

// scriptFonts adds nothing elsewhere: the system font lookup finds them.
func scriptFonts() []font.FontFace { return nil }
