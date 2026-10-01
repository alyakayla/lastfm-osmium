package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/ofabiodev/osmose/types"
)

const (
	chartTileSize    = 300
	chartMaxSize     = 9
	chartDefaultSize = 3
	chartDownloads   = 8 // concurrent cover downloads
)

var chartPeriods = map[string]struct{ api, label string }{
	"weekly":  {"7day", "Weekly"},
	"monthly": {"1month", "Monthly"},
	"yearly":  {"12month", "Yearly"},
	"alltime": {"overall", "All-time"},
}

const chartUsage = "Usage: /c [3x3 … 9x9] [weekly|monthly|yearly|alltime] [last.fm username]"

var titleFace = mustFace(gobold.TTF, 16)

func mustFace(ttf []byte, size float64) font.Face {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
	return face
}

// replies to "/c [size] [period] [username]" with a collage of the
// user's top albums. Arguments may come in any order.
func (b *bot) handleChart(ctx context.Context, message *types.Message, args []string) error {
	size, period, username := chartDefaultSize, "weekly", ""
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if _, ok := chartPeriods[lower]; ok {
			period = lower
			continue
		}
		if n, ok := parseChartSize(lower); ok {
			if n < 1 || n > chartMaxSize {
				_, err := message.Reply(ctx, fmt.Sprintf("Chart size must be between 1x1 and %dx%d.", chartMaxSize, chartMaxSize))
				return err
			}
			size = n
			continue
		}
		if username != "" {
			_, err := message.Reply(ctx, chartUsage)
			return err
		}
		username = arg
	}
	if username == "" {
		linked, ok, err := b.linkedUsername(ctx, message, "/c <size> <period> <last.fm username>")
		if !ok {
			return err
		}
		username = linked
	}

	if err := b.client.Chats.SetTyping(ctx, message.Chat, true); err != nil {
		log.Printf("chart: set typing: %v", err)
	}

	albums, err := b.lastfm.TopAlbums(ctx, username, chartPeriods[period].api, size*size)
	if errors.Is(err, errUserNotFound) {
		_, err := message.Reply(ctx, fmt.Sprintf("No Last.fm user named %q.", username))
		return err
	}
	if err != nil {
		log.Printf("chart %s: %v", username, err)
		_, err := message.Reply(ctx, "Couldn't reach Last.fm right now, try again later.")
		return err
	}
	if len(albums) == 0 {
		_, err := message.Reply(ctx, fmt.Sprintf("%s has no scrobbled albums for that period.", username))
		return err
	}

	img := renderChart(ctx, b.lastfm.http, albums, size)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return fmt.Errorf("encode chart: %w", err)
	}

	filename := fmt.Sprintf("%s-%s-%dx%d.jpg", username, period, size, size)
	file, err := b.uploadFile(ctx, filename, buf.Bytes())
	if err != nil {
		log.Printf("chart %s: %v", username, err)
		_, err := message.Reply(ctx, "Couldn't upload the chart, try again later.")
		return err
	}

	_, err = message.ReplyWith(ctx, types.MessageSendParams{
		Content: fmt.Sprintf("%s %dx%d chart for %s", chartPeriods[period].label, size, size, username),
		Media:   []*types.MediaRef{types.UploadedMedia(file, filename, "image/jpeg")},
	})
	return err
}

// parses "NxN" and returns N. ok is false when arg isn't a
// square size.
func parseChartSize(arg string) (n int, ok bool) {
	w, h, found := strings.Cut(arg, "x")
	if !found || w != h {
		return 0, false
	}
	n, err := strconv.Atoi(w)
	return n, err == nil
}

// draws albums on a size×size grid of cover tiles with the artist
// and album name on each. Missing or broken covers become blank tiles.
func renderChart(ctx context.Context, httpClient *http.Client, albums []lastfmAlbum, size int) image.Image {
	canvas := image.NewRGBA(image.Rect(0, 0, size*chartTileSize, size*chartTileSize))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{24, 24, 24, 255}), image.Point{}, draw.Src)

	covers := make([]image.Image, len(albums))
	sem := make(chan struct{}, chartDownloads)
	var wg sync.WaitGroup
	for i := range albums {
		url := albums[i].Cover()
		if url == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			cover, err := fetchImage(ctx, httpClient, url)
			if err != nil {
				log.Printf("chart: cover %s: %v", url, err)
				return
			}
			covers[i] = cover
		}()
	}
	wg.Wait()

	for i, album := range albums {
		tile := image.Rect(0, 0, chartTileSize, chartTileSize).
			Add(image.Pt((i%size)*chartTileSize, (i/size)*chartTileSize))
		if covers[i] != nil {
			xdraw.CatmullRom.Scale(canvas, tile, covers[i], covers[i].Bounds(), draw.Src, nil)
		}
		drawCaption(canvas, tile, album.Artist.Name, album.Name)
	}
	return canvas
}

func fetchImage(ctx context.Context, httpClient *http.Client, url string) (image.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", res.Status)
	}
	img, _, err := image.Decode(res.Body)
	return img, err
}

// writes the artist and album over a dark band at the top of tile.
func drawCaption(dst draw.Image, tile image.Rectangle, artist, album string) {
	const pad, lineHeight = 6, 19
	band := image.Rect(tile.Min.X, tile.Min.Y, tile.Max.X, tile.Min.Y+2*lineHeight+2*pad)
	draw.Draw(dst, band, image.NewUniform(color.RGBA{0, 0, 0, 150}), image.Point{}, draw.Over)

	d := &font.Drawer{Dst: dst, Src: image.White, Face: titleFace}
	maxWidth := fixed.I(tile.Dx() - 2*pad)
	for i, line := range []string{artist, album} {
		d.Dot = fixed.P(tile.Min.X+pad, tile.Min.Y+pad+(i+1)*lineHeight-4)
		d.DrawString(truncate(d, line, maxWidth))
	}
}

// shortens s with an ellipsis so it fits within width.
func truncate(d *font.Drawer, s string, width fixed.Int26_6) string {
	if d.MeasureString(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		if candidate := string(runes) + "…"; d.MeasureString(candidate) <= width {
			return candidate
		}
	}
	return ""
}
