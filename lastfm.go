package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const lastfmAPIURL = "https://ws.audioscrobbler.com/2.0/"

// is returned when Last.fm has no user with the given name.
var errNotFound = errors.New("last.fm: not found")

type lastfmClient struct {
	apiKey string
	http   *http.Client
}

func newLastfmClient(apiKey string) *lastfmClient {
	return &lastfmClient{apiKey: apiKey, http: &http.Client{Timeout: 10 * time.Second}}
}

type lastfmImage struct {
	Size string `json:"size"`
	URL  string `json:"#text"`
}

type lastfmUser struct {
	Name        string        `json:"name"`
	RealName    string        `json:"realname"`
	URL         string        `json:"url"`
	Country     string        `json:"country"`
	Subscriber  string        `json:"subscriber"`
	Type        string        `json:"type"`
	Playcount   string        `json:"playcount"`
	ArtistCount string        `json:"artist_count"`
	AlbumCount  string        `json:"album_count"`
	TrackCount  string        `json:"track_count"`
	Images      []lastfmImage `json:"image"`
	Registered  struct {
		Unixtime string `json:"unixtime"`
	} `json:"registered"`
}

// returns the largest available avatar URL, or "" when the user has none.
func (u *lastfmUser) Avatar() string { return largestImage(u.Images) }

// returns the last non-empty URL; Last.fm lists sizes small to large.
func largestImage(images []lastfmImage) string {
	for i := len(images) - 1; i >= 0; i-- {
		if images[i].URL != "" {
			return images[i].URL
		}
	}
	return ""
}

// returns the account creation time, or the zero time if unknown.
func (u *lastfmUser) RegisteredAt() time.Time {
	sec, err := strconv.ParseInt(u.Registered.Unixtime, 10, 64)
	if err != nil || sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

type lastfmTrack struct {
	Name   string `json:"name"`
	Artist struct {
		Name string `json:"#text"`
	} `json:"artist"`
	Attr struct {
		NowPlaying string `json:"nowplaying"`
	} `json:"@attr"`
}

func (t *lastfmTrack) NowPlaying() bool { return t.Attr.NowPlaying == "true" }

// fetches a user's profile via user.getInfo.
func (c *lastfmClient) UserInfo(ctx context.Context, username string) (*lastfmUser, error) {
	var resp struct {
		User lastfmUser `json:"user"`
	}
	if err := c.call(ctx, "user.getinfo", url.Values{"user": {username}}, &resp); err != nil {
		return nil, err
	}
	return &resp.User, nil
}

// returns the user's current or most recently scrobbled track, or
// nil when they have never scrobbled.
func (c *lastfmClient) LatestTrack(ctx context.Context, username string) (*lastfmTrack, error) {
	var resp struct {
		RecentTracks struct {
			Track json.RawMessage `json:"track"`
		} `json:"recenttracks"`
	}
	params := url.Values{"user": {username}, "limit": {"1"}}
	if err := c.call(ctx, "user.getrecenttracks", params, &resp); err != nil {
		return nil, err
	}
	tracks, err := decodeList[lastfmTrack](resp.RecentTracks.Track)
	if err != nil {
		return nil, fmt.Errorf("decode recent tracks: %w", err)
	}
	if len(tracks) == 0 {
		return nil, nil
	}
	return &tracks[0], nil
}

type lastfmAlbum struct {
	Name      string `json:"name"`
	Playcount string `json:"playcount"`
	Artist    struct {
		Name string `json:"name"`
	} `json:"artist"`
	Images []lastfmImage `json:"image"`
}

// returns the largest available cover URL, or "" when there is none.
func (a *lastfmAlbum) Cover() string { return largestImage(a.Images) }

// fetches a user's most played albums via user.getTopAlbums. period
// is one of Last.fm's periods: overall, 7day, 1month, 3month, 6month, 12month.
func (c *lastfmClient) TopAlbums(ctx context.Context, username, period string, limit int) ([]lastfmAlbum, error) {
	var resp struct {
		TopAlbums struct {
			Album json.RawMessage `json:"album"`
		} `json:"topalbums"`
	}
	params := url.Values{"user": {username}, "period": {period}, "limit": {strconv.Itoa(limit)}}
	if err := c.call(ctx, "user.gettopalbums", params, &resp); err != nil {
		return nil, err
	}
	albums, err := decodeList[lastfmAlbum](resp.TopAlbums.Album)
	if err != nil {
		return nil, fmt.Errorf("decode top albums: %w", err)
	}
	// accounting for occasional more than limit returns.
	if len(albums) > limit {
		albums = albums[:limit]
	}
	return albums, nil
}

// returns Last.fm's corrected spelling of artist and how many times username
// has scrobbled it.
func (c *lastfmClient) ArtistPlays(ctx context.Context, artist, username string) (name string, plays int, err error) {
	var resp struct {
		Artist struct {
			Name  string `json:"name"`
			Stats struct {
				UserPlaycount string `json:"userplaycount"`
			} `json:"stats"`
		} `json:"artist"`
	}
	params := url.Values{"artist": {artist}, "username": {username}, "autocorrect": {"1"}}
	if err := c.call(ctx, "artist.getinfo", params, &resp); err != nil {
		return "", 0, err
	}
	// The count is missing when the user has never scrobbled the artist.
	plays, _ = strconv.Atoi(resp.Artist.Stats.UserPlaycount)
	return resp.Artist.Name, plays, nil
}

// decodes a Last.fm list field, which is an object instead of an
// array when it holds a single item, and missing when it holds none.
func decodeList[T any](raw json.RawMessage) ([]T, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var items []T
	if err := json.Unmarshal(raw, &items); err == nil {
		return items, nil
	}
	var item T
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, err
	}
	return []T{item}, nil
}

func (c *lastfmClient) call(ctx context.Context, method string, params url.Values, out any) error {
	params.Set("method", method)
	params.Set("api_key", c.apiKey)
	params.Set("format", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lastfmAPIURL+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	var body json.RawMessage
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return fmt.Errorf("%s: decode response: %w", method, err)
	}

	var apiErr struct {
		Code    int    `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Code != 0 {
		if apiErr.Code == 6 {
			return errNotFound
		}
		return fmt.Errorf("%s: last.fm error %d: %s", method, apiErr.Code, apiErr.Message)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: unexpected status %s", method, res.Status)
	}
	return json.Unmarshal(body, out)
}
