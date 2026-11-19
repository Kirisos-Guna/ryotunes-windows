// Package desktopstyle lets Ryotunes choose the Ryoku look itself where no
// Ryoku desktop does (Windows).
//
// Ryotunes wears the look the Ryoku desktop chose in Ryoku Settings' Style
// page: the Lithic library reads it from ryoku/shell.json and
// ryoku/matugen.json in the config directory (palette.ReadDesktop). Windows
// has no Ryoku Settings to write them, so Appearance offered no choice and
// always showed Lithic. Here Ryotunes writes the same files, in the same
// words, and the library reads them as it would the desktop's.
package desktopstyle

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/ryoku-dev/ryoku/docs/lithic/library/go/design"
)

// Editable reports that Ryotunes chooses the look itself: where there is no
// Ryoku desktop to choose it.
func Editable() bool { return runtime.GOOS == "windows" }

// dir is the ryoku config directory, as the library finds it.
func dir() (string, error) {
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "ryoku"), nil
}

// SetDesign chooses Lithic, Material, Adwaita or Fluent.
func SetDesign(style design.Style) error {
	return updateTheme(func(theme map[string]any) { theme["design"] = style.String() })
}

// SetHighContrast chooses high or standard contrast.
func SetHighContrast(high bool) error {
	return updateTheme(func(theme map[string]any) {
		theme["contrast"] = "standard"
		if high {
			theme["contrast"] = "high"
		}
	})
}

// SetReduceMotion stills the animations, or lets them run.
func SetReduceMotion(reduce bool) error {
	return updateTheme(func(theme map[string]any) {
		motion, _ := theme["motion"].(map[string]any)
		if motion == nil {
			motion = map[string]any{}
		}
		motion["reduce"] = reduce
		theme["motion"] = motion
	})
}

// SetLight chooses the light or the dark look (matugen.json's mode).
func SetLight(light bool) error {
	return update("matugen.json", func(knobs map[string]any) {
		knobs["mode"] = "dark"
		if light {
			knobs["mode"] = "light"
		}
	})
}

// updateTheme changes shell.json's theme object, keeping everything else.
func updateTheme(change func(theme map[string]any)) error {
	return update("shell.json", func(shell map[string]any) {
		theme, _ := shell["theme"].(map[string]any)
		if theme == nil {
			theme = map[string]any{}
		}
		change(theme)
		shell["theme"] = theme
	})
}

func update(name string, change func(map[string]any)) error {
	folder, err := dir()
	if err != nil {
		return err
	}
	path := filepath.Join(folder, name)
	object := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, &object) != nil || object == nil {
			object = map[string]any{} // unreadable: start over rather than fail
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	change(object)
	raw, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
