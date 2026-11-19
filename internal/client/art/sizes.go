package art

import (
	"regexp"
	"strconv"
	"strings"
)

// Services list a cover at a few sizes and Ryotunes often gets the smallest
// (Spotify's 64 px album cover): drawn large, on Now playing or Home, it was
// a blur of big pixels. sizedSourceURL asks the service for the same picture
// at a size at least as large as it is drawn, when the URL says how.

// Spotify image ids start with a 16-hex-digit prefix naming the size.
var spotifySizes = []struct {
	kind  string // the family of the picture
	sizes []spotifySize
}{
	{"ab67616d", []spotifySize{{64, "00004851"}, {300, "00001e02"}, {640, "0000b273"}}},  // album covers
	{"ab676161", []spotifySize{{160, "0000f178"}, {320, "00005174"}, {640, "0000e5eb"}}}, // artist pictures
}

type spotifySize struct {
	pixels int
	code   string
}

// Google image URLs (YouTube Music covers and channel pictures) end in size
// options: "=w60-h60-l90-rj" or "=s88-c-k-c0x00ffffff-no-rj".
var googleSize = regexp.MustCompile(`=(w\d+-h\d+|s\d+)([-a-z0-9]*)$`)

func sizedSourceURL(rawURL string, size int) string {
	if size <= 0 {
		return rawURL
	}
	if rest, ok := strings.CutPrefix(rawURL, "https://i.scdn.co/image/"); ok && len(rest) >= 16 {
		for _, family := range spotifySizes {
			if !strings.HasPrefix(rest, family.kind) {
				continue
			}
			current := rest[8:16]
			known := false
			for _, candidate := range family.sizes {
				known = known || candidate.code == current
			}
			if !known {
				return rawURL
			}
			pick := family.sizes[len(family.sizes)-1]
			for _, candidate := range family.sizes {
				if candidate.pixels >= size {
					pick = candidate
					break
				}
			}
			return "https://i.scdn.co/image/" + family.kind + pick.code + rest[16:]
		}
		return rawURL
	}
	if strings.Contains(rawURL, "googleusercontent.com/") || strings.Contains(rawURL, "ggpht.com/") {
		pixels := strconv.Itoa(max(size, 60))
		return googleSize.ReplaceAllStringFunc(rawURL, func(match string) string {
			parts := googleSize.FindStringSubmatch(match)
			if strings.HasPrefix(parts[1], "w") {
				return "=w" + pixels + "-h" + pixels + parts[2]
			}
			return "=s" + pixels + parts[2]
		})
	}
	return rawURL
}
