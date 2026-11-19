package desktopstyle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ryoku-dev/ryoku/docs/lithic/library/go/design"
	"github.com/ryoku-dev/ryoku/docs/lithic/library/go/palette"
)

// What Ryotunes writes reads back through the library as the desktop's look,
// and changing one choice keeps the others and the file's other settings.
func TestChoicesReadBackAsTheDesktopLook(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	if err := os.MkdirAll(filepath.Join(config, "ryoku"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "ryoku", "shell.json"), []byte(`{"showJapaneseGloss": false, "theme": {"tint": "low"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, set := range []func() error{
		func() error { return SetDesign(design.Fluent) },
		func() error { return SetHighContrast(true) },
		func() error { return SetReduceMotion(true) },
		func() error { return SetLight(true) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	desktop := palette.ReadDesktop(palette.WithDirs(config, t.TempDir(), filepath.Join(t.TempDir(), "os-release")))
	if desktop.Design != design.Fluent || desktop.Look.Contrast != palette.High || !desktop.ReduceMotion || desktop.Mode != palette.Light {
		t.Fatalf("desktop read back = design %v contrast %v reduce %v mode %v", desktop.Design, desktop.Look.Contrast, desktop.ReduceMotion, desktop.Mode)
	}
	if desktop.Gloss || desktop.Look.Tint != palette.TintLow {
		t.Fatalf("other settings were lost: gloss %v tint %v", desktop.Gloss, desktop.Look.Tint)
	}
	if err := SetDesign(design.Material); err != nil {
		t.Fatal(err)
	}
	if desktop := palette.ReadDesktop(palette.WithDirs(config, t.TempDir(), filepath.Join(t.TempDir(), "os-release"))); desktop.Design != design.Material || desktop.Look.Contrast != palette.High {
		t.Fatalf("after a second change: design %v contrast %v", desktop.Design, desktop.Look.Contrast)
	}
}
