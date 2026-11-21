package spotify

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
	playlist4pb "github.com/devgianlu/go-librespot/proto/spotify/playlist4"
	"github.com/devgianlu/go-librespot/session"
	"github.com/devgianlu/go-librespot/spclient"
	"github.com/ryoku-dev/ryotunes/internal/model"
	providerapi "github.com/ryoku-dev/ryotunes/internal/provider"
	"google.golang.org/protobuf/proto"
)

// Playlist implements provider.Provider.
func (p *Spotify) Playlist(ctx context.Context, id string, ordering model.PlaylistSort, desc bool) (model.PlaylistPage, error) {
	if id == "spotify:playlist:liked" || id == "liked" {
		sess, err := p.session(ctx)
		if err != nil {
			return model.PlaylistPage{}, err
		}
		liked, err := p.likedTracks(ctx, sess)
		if err != nil {
			return model.PlaylistPage{}, mapProviderError(err)
		}
		title := "Liked Songs"
		return model.PlaylistPage{Title: &title, Subtitle: songCount(len(liked)), Items: liked, Owned: true}, nil
	}
	bare, err := bareID(id, "playlist")
	if err != nil {
		return model.PlaylistPage{}, err
	}
	sess, err := p.session(ctx)
	if err != nil {
		return model.PlaylistPage{}, err
	}
	content, err := p.playlistSnapshot(ctx, sess.Spclient(), bare)
	if err != nil {
		return model.PlaylistPage{}, mapProviderError(err)
	}
	attrs := content.GetAttributes()
	page := model.PlaylistPage{Title: new(attrs.GetName()), Description: new(attrs.GetDescription()), Owned: content.GetOwnerUsername() == sess.Username(), Collaborative: attrs.GetCollaborative()}
	page.Thumbnail = playlistCover(attrs)
	page.Cover = page.Thumbnail
	items := content.GetContents().GetItems()
	uris := make([]string, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(item.GetUri(), "spotify:track:") {
			uris = append(uris, item.GetUri())
		}
	}
	known, err := p.trackMetadata(ctx, sess, uris)
	if err != nil {
		return page, mapProviderError(err)
	}
	for _, item := range items {
		track, ok := known[item.GetUri()]
		if !ok {
			continue
		}
		attrs := item.GetAttributes()
		if id := attrs.GetItemId(); len(id) > 0 {
			track.SetVideoID = new(hex.EncodeToString(id))
		}
		if by := attrs.GetAddedBy(); by != "" {
			track.AddedBy = new(by)
		}
		page.Items = append(page.Items, track)
	}
	sortPlaylist(page.Items, ordering, desc)
	page.Subtitle = songCount(len(page.Items))
	return page, nil
}

// PlaylistMore implements provider.Provider; playlist snapshots are fetched in full.
func (p *Spotify) PlaylistMore(context.Context, string) (model.PlaylistTracksPage, error) {
	return model.PlaylistTracksPage{}, nil
}

func (p *Spotify) playlistSnapshot(ctx context.Context, sp spclientRequester, id string) (*playlist4pb.SelectedListContent, error) {
	resp, err := sp.Request(ctx, http.MethodGet, "/playlist/v2/playlist/"+id, url.Values{"from": {"0"}, "length": {"10000"}}, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("playlist status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var content playlist4pb.SelectedListContent
	if err := proto.Unmarshal(body, &content); err != nil {
		return nil, err
	}
	return &content, nil
}

type spclientRequester interface {
	Request(context.Context, string, string, url.Values, http.Header, []byte) (*http.Response, error)
}

func playlistCover(attrs *playlist4pb.ListAttributes) *string {
	if attrs == nil {
		return nil
	}
	for _, size := range attrs.GetPictureSize() {
		if u := size.GetUrl(); strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			return new(u)
		}
	}
	if len(attrs.GetPicture()) > 0 {
		return new("https://i.scdn.co/image/" + hex.EncodeToString(attrs.GetPicture()))
	}
	return nil
}

func sortPlaylist(items []model.Track, ordering model.PlaylistSort, desc bool) {
	less := func(i, j int) bool { return false }
	switch ordering {
	case model.PlaylistSortTitle:
		less = func(i, j int) bool { return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title) }
	case model.PlaylistSortArtist:
		less = func(i, j int) bool { return strings.ToLower(items[i].Artists) < strings.ToLower(items[j].Artists) }
	case model.PlaylistSortAlbum:
		less = func(i, j int) bool {
			a, b := "", ""
			if items[i].Album != nil {
				a = *items[i].Album
			}
			if items[j].Album != nil {
				b = *items[j].Album
			}
			return strings.ToLower(a) < strings.ToLower(b)
		}
	default:
		if desc {
			reverseTracks(items)
		}
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		if desc {
			return less(j, i)
		}
		return less(i, j)
	})
}
func reverseTracks(items []model.Track) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

// Library implements provider.Provider.
func (p *Spotify) Library(ctx context.Context) (model.Library, error) {
	sess, err := p.session(ctx)
	if err != nil {
		return model.Library{}, err
	}
	library := model.Library{}
	// Liked songs, the playlists, saved albums and followed artists load
	// at once: one after another they took the sum of their times.
	var tracks []model.Track
	var root *playlist4pb.SelectedListContent
	var albums, artists []model.BrowseItem
	var likedErr, rootErr, albumsErr, artistsErr error
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); tracks, likedErr = p.likedTracks(ctx, sess) }()
	go func() {
		defer wg.Done()
		root, rootErr = sess.Spclient().Rootlist(ctx, sess.Username(), 0, 10000)
	}()
	go func() { defer wg.Done(); albums, albumsErr = p.savedAlbums(ctx, sess) }()
	go func() { defer wg.Done(); artists, artistsErr = p.savedArtists(ctx, sess) }()
	wg.Wait()
	if likedErr == nil {
		library.Liked = tracks
	} else {
		p.log.Warn("Spotify liked songs failed", "error", likedErr)
	}
	if rootErr == nil {
		library.Playlists = mapRootlist(root)
		// Own playlists name their owner by account ID; show the name.
		p.mu.RLock()
		name := p.name
		p.mu.RUnlock()
		if name != "" {
			for index := range library.Playlists {
				if subtitle := library.Playlists[index].Subtitle; subtitle != nil && *subtitle == sess.Username() {
					library.Playlists[index].Subtitle = new(name)
				}
			}
		}
	} else {
		p.log.Warn("Spotify rootlist failed", "error", rootErr)
		if likedErr != nil {
			// Nothing of the library loaded: report why.
			return library, mapProviderError(likedErr)
		}
	}
	if albumsErr == nil {
		library.Albums = albums
	} else {
		p.log.Warn("Spotify saved albums failed", "error", albumsErr)
	}
	if artistsErr == nil {
		library.Artists = artists
	} else {
		p.log.Warn("Spotify saved artists failed", "error", artistsErr)
	}
	return library, nil
}

// savedAlbums reads them as the Web Player does where it runs, else through
// the Web API.
func (p *Spotify) savedAlbums(ctx context.Context, sess *session.Session) ([]model.BrowseItem, error) {
	if webPlayback() {
		return p.libraryV3Items(ctx, sess, "Albums")
	}
	return p.webSavedAlbums(ctx, sess)
}

// savedArtists reads them as the Web Player does where it runs, else through
// the Web API.
func (p *Spotify) savedArtists(ctx context.Context, sess *session.Session) ([]model.BrowseItem, error) {
	if webPlayback() {
		return p.libraryV3Items(ctx, sess, "Artists")
	}
	return p.webSavedArtists(ctx, sess)
}

func mapRootlist(root *playlist4pb.SelectedListContent) []model.BrowseItem {
	if root == nil {
		return nil
	}
	contents := root.GetContents()
	items := contents.GetItems()
	meta := contents.GetMetaItems()
	out := make([]model.BrowseItem, 0, len(items))
	for index, item := range items {
		uri := item.GetUri()
		if !strings.HasPrefix(uri, "spotify:playlist:") {
			continue
		}
		var detail *playlist4pb.MetaItem
		if index < len(meta) {
			detail = meta[index]
		}
		title := "Unknown"
		var thumb *string
		var subtitle *string
		if detail != nil {
			if n := detail.GetAttributes().GetName(); n != "" {
				title = n
			}
			thumb = playlistCover(detail.GetAttributes())
			if owner := detail.GetOwnerUsername(); owner != "" {
				subtitle = new(owner)
			}
		}
		out = append(out, model.BrowseItem{Kind: "playlist", ID: uri, Title: title, Subtitle: subtitle, Thumbnail: thumb})
	}
	return out
}

type webArtist struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Images []struct {
		URL string `json:"url"`
	} `json:"images"`
}
type webAlbum struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Artists []webArtist `json:"artists"`
	Images  []struct {
		URL string `json:"url"`
	} `json:"images"`
}
type webTrack struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Artists  []webArtist `json:"artists"`
	Album    webAlbum    `json:"album"`
	Duration int64       `json:"duration_ms"`
	Explicit bool        `json:"explicit"`
}

// likedTracks lists Liked Songs, newest first. It reads the collection
// through Spotify's client API (the context resolver and batched metadata,
// as the playlists do), which is not subject to the public Web API's tight
// rate limit for librespot's shared client ID: that limit answered 429 and
// left the Liked Songs page empty. The Web API remains the fallback.
func (p *Spotify) likedTracks(ctx context.Context, sess *session.Session) ([]model.Track, error) {
	tracks, err := p.collectionTracks(ctx, sess)
	if err == nil {
		return tracks, nil
	}
	if errors.Is(err, errEmptyCollection) {
		return []model.Track{}, nil
	}
	p.log.Warn("Spotify collection context failed, using the Web API", "error", err)
	return p.webSavedTracks(ctx, sess)
}

var errEmptyCollection = errors.New("Spotify collection is empty")

func (p *Spotify) collectionTracks(ctx context.Context, sess *session.Session) ([]model.Track, error) {
	uri := "spotify:user:" + sess.Username() + ":collection"
	resolver, err := spclient.NewContextResolver(ctx, p.liblog, sess.Spclient(), &connectpb.Context{Uri: uri})
	if err != nil {
		if strings.Contains(err.Error(), "has no tracks") {
			return nil, errEmptyCollection
		}
		return nil, err
	}
	var uris []string
	seen := map[string]bool{}
	for index := 0; ; index++ {
		page, err := resolver.Page(ctx, index)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		for _, track := range page {
			trackURI := track.GetUri()
			if strings.HasPrefix(trackURI, "spotify:track:") && !seen[trackURI] {
				seen[trackURI] = true
				uris = append(uris, trackURI)
			}
		}
	}
	if len(uris) == 0 {
		return []model.Track{}, nil
	}
	known, err := p.trackMetadata(ctx, sess, uris)
	if err != nil {
		return nil, err
	}
	out := make([]model.Track, 0, len(uris))
	for _, trackURI := range uris {
		if track, ok := known[trackURI]; ok {
			out = append(out, track)
		}
	}
	return out, nil
}

func (p *Spotify) webSavedTracks(ctx context.Context, sess spotifySession) ([]model.Track, error) {
	endpoint := "https://api.spotify.com/v1/me/tracks?limit=50"
	var out []model.Track
	for endpoint != "" {
		var page struct {
			Items []struct {
				Track webTrack `json:"track"`
			} `json:"items"`
			Next string `json:"next"`
		}
		if err := p.webJSON(ctx, sess, endpoint, &page); err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			out = append(out, mapWebTrack(item.Track))
		}
		endpoint = page.Next
	}
	return out, nil
}
func (p *Spotify) webSavedAlbums(ctx context.Context, sess spotifySession) ([]model.BrowseItem, error) {
	endpoint := "https://api.spotify.com/v1/me/albums?limit=50"
	var out []model.BrowseItem
	for endpoint != "" {
		var page struct {
			Items []struct {
				Album webAlbum `json:"album"`
			} `json:"items"`
			Next string `json:"next"`
		}
		if err := p.webJSON(ctx, sess, endpoint, &page); err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			card := model.BrowseItem{Kind: "album", ID: "spotify:album:" + item.Album.ID, Title: item.Album.Name}
			if len(item.Album.Artists) > 0 {
				card.Subtitle = new(item.Album.Artists[0].Name)
			}
			if len(item.Album.Images) > 0 {
				card.Thumbnail = new(item.Album.Images[len(item.Album.Images)-1].URL)
			}
			out = append(out, card)
		}
		endpoint = page.Next
	}
	return out, nil
}
func (p *Spotify) webSavedArtists(ctx context.Context, sess spotifySession) ([]model.BrowseItem, error) {
	endpoint := "https://api.spotify.com/v1/me/following?type=artist&limit=50"
	var out []model.BrowseItem
	for endpoint != "" {
		var page struct {
			Artists struct {
				Items []webArtist `json:"items"`
				Next  string      `json:"next"`
			} `json:"artists"`
		}
		if err := p.webJSON(ctx, sess, endpoint, &page); err != nil {
			return nil, err
		}
		for _, artist := range page.Artists.Items {
			card := model.BrowseItem{Kind: "artist", ID: "spotify:artist:" + artist.ID, Title: artist.Name}
			if len(artist.Images) > 0 {
				card.Thumbnail = new(artist.Images[len(artist.Images)-1].URL)
			}
			out = append(out, card)
		}
		endpoint = page.Artists.Next
	}
	return out, nil
}

type spotifySession interface{ Spclient() *spclient.Spclient }

// webRetryLimit is the longest Retry-After webJSON waits out; a longer
// rate-limit window is reported instead of holding the page open.
const webRetryLimit = 10 * time.Second

func (p *Spotify) webJSON(ctx context.Context, sess spotifySession, endpoint string, target any) error {
	for attempt := 0; ; attempt++ {
		wait, err := p.webJSONOnce(ctx, sess, endpoint, target)
		if wait == 0 {
			return err
		}
		if attempt >= 2 || wait > webRetryLimit {
			return fmt.Errorf("Spotify is limiting requests right now. Try again in %s.", wait.Round(time.Second))
		}
		p.log.Warn("Spotify Web API rate limited, retrying", "wait", wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// webJSONOnce makes one request; a positive wait means Spotify answered 429
// and asks to be called again after it.
func (p *Spotify) webJSONOnce(ctx context.Context, sess spotifySession, endpoint string, target any) (time.Duration, error) {
	token, err := sess.Spclient().GetAccessToken(ctx, false)
	if err != nil {
		return 0, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return retryAfter(resp.Header.Get("Retry-After"), time.Now()), nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return 0, providerapi.ErrSignInRequired
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("Spotify Web API status %d", resp.StatusCode)
	}
	return 0, json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(target)
}
func mapWebTrack(raw webTrack) model.Track {
	artists := make([]model.Artist, 0, len(raw.Artists))
	for _, a := range raw.Artists {
		artists = append(artists, model.Artist{Text: a.Name, ID: new("spotify:artist:" + a.ID)})
	}
	track := model.Track{VideoID: "spotify:track:" + raw.ID, Title: raw.Name, Artists: artistNames(artists), ArtistRuns: artists, Explicit: raw.Explicit}
	if len(artists) > 0 {
		track.ArtistID = artists[0].ID
	}
	if raw.Album.ID != "" {
		track.AlbumID = new("spotify:album:" + raw.Album.ID)
		track.Album = new(raw.Album.Name)
	}
	if len(raw.Album.Images) > 0 {
		track.Thumbnail = new(raw.Album.Images[len(raw.Album.Images)-1].URL)
	}
	if raw.Duration > 0 {
		track.Duration = new(durationLabel(time.Duration(raw.Duration) * time.Millisecond))
	}
	return track
}

// Account implements provider.Provider.
func (p *Spotify) Account(ctx context.Context) (model.Account, error) {
	p.mu.RLock()
	signedIn := p.sess != nil
	p.mu.RUnlock()
	if !signedIn {
		return model.Account{SignedIn: false}, nil
	}
	if _, err := p.session(ctx); err != nil {
		return model.Account{}, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return model.Account{SignedIn: true, Name: new(p.name)}, nil
}

// Next implements provider.Provider with Spotify track radio.
func (p *Spotify) Next(ctx context.Context, seed model.Track, played []string) ([]model.Track, error) {
	tracks, _, err := p.StartRadio(ctx, "track", seed.VideoID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(played)+1)
	seen[seed.VideoID] = true
	for _, id := range played {
		seen[id] = true
	}
	out := tracks[:0]
	for _, track := range tracks {
		if !seen[track.VideoID] {
			seen[track.VideoID] = true
			out = append(out, track)
		}
	}
	return out, nil
}
