package spotify

import (
	"context"
	"strings"

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

// libraryV3Items lists one library filter ("Albums" or "Artists").
func (p *Spotify) libraryV3Items(ctx context.Context, sess *session.Session, filter string) ([]model.BrowseItem, error) {
	var out []model.BrowseItem
	for offset := 0; offset < 2000; offset += libraryPage {
		var data libraryV3Data
		variables := map[string]any{
			"filters": []string{filter}, "order": nil, "textFilter": "",
			"features": []string{"LIKED_SONGS", "YOUR_EPISODES_V2", "PRERELEASES", "PRERELEASES_V2", "CLIPS", "EVENTS"},
			"limit":    libraryPage, "offset": offset,
			"flatten": false, "expandedFolders": []string{}, "folderUri": nil, "includeFoldersWhenFlattening": true,
		}
		if err := p.pathfinderQuery(ctx, sess, "libraryV3", variables, &data); err != nil {
			return out, err
		}
		if data.Me == nil || data.Me.Library == nil {
			break
		}
		rows := data.Me.Library.Items
		for _, row := range rows {
			if item, ok := libraryV3Item(row.Item.Data.Typename, row.Item.Data.URI, row.Item.Data.Name, row.Item.Data.Profile.Name, row.Item.Data.Artists, row.Item.Data.Cover, row.Item.Data.Visuals.Avatar); ok {
				out = append(out, item)
			}
		}
		if len(rows) == 0 || offset+len(rows) >= data.Me.Library.TotalCount {
			break
		}
	}
	return out, nil
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
