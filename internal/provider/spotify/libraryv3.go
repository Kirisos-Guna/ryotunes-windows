package spotify

import (
	"context"
	"strings"
	"sync"

	"github.com/devgianlu/go-librespot/session"

	"github.com/ryoku-dev/ryotunes/internal/model"
)

// Saved albums and followed artists, read as the Web Player reads "Your
// Library" (Pathfinder's libraryV3 query, sent by the Web Player page). The
// public Web API answers librespot's shared client ID "429 Too Many
// Requests" for hours, which left them out of the library.

type libraryV3Data struct {
	Me *struct {
		Library *struct {
			Items []struct {
				Item struct {
					Data struct {
						Typename string    `json:"__typename"`
						URI      string    `json:"uri"`
						Name     string    `json:"name"`
						Artists  pfArtists `json:"artists"`
						Cover    pfArtwork `json:"coverArt"`
						Profile  struct {
							Name string `json:"name"`
						} `json:"profile"`
						Visuals struct {
							Avatar pfArtwork `json:"avatarImage"`
						} `json:"visuals"`
					} `json:"data"`
				} `json:"item"`
			} `json:"items"`
			TotalCount int `json:"totalCount"`
		} `json:"libraryV3"`
	} `json:"me"`
}

const libraryPage = 50

// libraryV3Items lists one library filter ("Albums" or "Artists"): the first
// page, then the rest four pages at a time, in order.
func (p *Spotify) libraryV3Items(ctx context.Context, sess *session.Session, filter string) ([]model.BrowseItem, error) {
	first, total, err := p.libraryV3Page(ctx, sess, filter, 0)
	if err != nil {
		return nil, err
	}
	pages := [][]model.BrowseItem{first}
	for offset := libraryPage; offset < min(total, libraryMax); offset += libraryPage * libraryParallel {
		batch := make([][]model.BrowseItem, libraryParallel)
		errs := make([]error, libraryParallel)
		var wg sync.WaitGroup
		for i := range libraryParallel {
			at := offset + i*libraryPage
			if at >= min(total, libraryMax) {
				break
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				batch[i], _, errs[i] = p.libraryV3Page(ctx, sess, filter, at)
			}()
		}
		wg.Wait()
		for i := range libraryParallel {
			if errs[i] != nil {
				return flatten(pages), errs[i]
			}
			pages = append(pages, batch[i])
		}
	}
	return flatten(pages), nil
}

const (
	libraryParallel = 4
	libraryMax      = 2000
)

// libraryV3Page reads one page of a library filter and the filter's total.
func (p *Spotify) libraryV3Page(ctx context.Context, sess *session.Session, filter string, offset int) ([]model.BrowseItem, int, error) {
	var data libraryV3Data
	variables := map[string]any{
		"filters": []string{filter}, "order": nil, "textFilter": "",
		"features": []string{"LIKED_SONGS", "YOUR_EPISODES_V2", "PRERELEASES", "PRERELEASES_V2", "CLIPS", "EVENTS"},
		"limit":    libraryPage, "offset": offset,
		"flatten": false, "expandedFolders": []string{}, "folderUri": nil, "includeFoldersWhenFlattening": true,
	}
	if err := p.pathfinderQuery(ctx, sess, "libraryV3", variables, &data); err != nil {
		return nil, 0, err
	}
	if data.Me == nil || data.Me.Library == nil {
		return nil, 0, nil
	}
	var out []model.BrowseItem
	for _, row := range data.Me.Library.Items {
		d := row.Item.Data
		if item, ok := libraryV3Item(d.Typename, d.URI, d.Name, d.Profile.Name, d.Artists, d.Cover, d.Visuals.Avatar); ok {
			out = append(out, item)
		}
	}
	return out, data.Me.Library.TotalCount, nil
}

func flatten(pages [][]model.BrowseItem) []model.BrowseItem {
	var out []model.BrowseItem
	for _, page := range pages {
		out = append(out, page...)
	}
	return out
}

func libraryV3Item(typename, uri, name, profileName string, artists pfArtists, cover, avatar pfArtwork) (model.BrowseItem, bool) {
	switch {
	case typename == "Album" || strings.HasPrefix(uri, "spotify:album:"):
		if name == "" {
			return model.BrowseItem{}, false
		}
		item := model.BrowseItem{Kind: "album", ID: uri, Title: name, Thumbnail: imageFrom(cover.Sources, false)}
		item.ArtistRuns = mapArtists(artists)
		if names := artistNames(item.ArtistRuns); names != "" {
			item.Subtitle = new(names)
		}
		return item, true
	case typename == "Artist" || strings.HasPrefix(uri, "spotify:artist:"):
		if profileName == "" {
			profileName = name
		}
		if profileName == "" {
			return model.BrowseItem{}, false
		}
		return model.BrowseItem{Kind: "artist", ID: uri, Title: profileName, Thumbnail: imageFrom(avatar.Sources, false)}, true
	}
	return model.BrowseItem{}, false
}
