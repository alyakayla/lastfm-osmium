package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/ofabiodev/osmose/types"
)

func (b *bot) handleBio(ctx context.Context, message *types.Message, args []string) error {
	var username string
	switch len(args) {
	case 0:
		linked, ok, err := b.linkedUsername(ctx, message, ".bio <last.fm username>")
		if !ok {
			return err
		}
		username = linked
	case 1:
		username = args[0]
	default:
		_, err := message.Reply(ctx, "Usage: .bio [last.fm username]")
		return err
	}

	user, err := b.lastfm.UserInfo(ctx, username)
	if errors.Is(err, errNotFound) {
		_, err := message.Reply(ctx, fmt.Sprintf("No Last.fm user named %q.", username))
		return err
	}
	if err != nil {
		log.Printf("bio %s: %v", username, err)
		_, err := message.Reply(ctx, "Couldn't reach Last.fm right now, try again later.")
		return err
	}

	track, err := b.lastfm.LatestTrack(ctx, user.Name)

	params := types.MessageSendParams{
		Content: formatBio(user, track),
		BotInfo: &types.MessageBotInfo{
			Buttons: types.MessageButtons{{{Label: "View on Last.fm", URL: user.URL}}},
		},
	}
	if avatar := user.Avatar(); avatar != "" {
		params.Media = []*types.MediaRef{types.EmbedMedia(avatar)}
	}
	_, err = message.ReplyWith(ctx, params)
	return err
}

func formatBio(user *lastfmUser, track *lastfmTrack) string {
	var b strings.Builder

	b.WriteString(user.Name)
	if user.RealName != "" {
		fmt.Fprintf(&b, " (%s)", user.RealName)
	}
	if user.Subscriber == "1" {
		b.WriteString(" ⭐ Pro")
	}
	if user.Type != "" && user.Type != "user" && user.Type != "subscriber" {
		fmt.Fprintf(&b, " · %s", user.Type)
	}
	b.WriteString("\n")

	if user.Country != "" && user.Country != "None" {
		fmt.Fprintf(&b, "📍 %s\n", user.Country)
	}
	if registered := user.RegisteredAt(); !registered.IsZero() {
		fmt.Fprintf(&b, "📅 Scrobbling since %s\n", registered.Format("Jan 2, 2006"))
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "🎧 Scrobbles: %s\n", formatCount(user.Playcount))
	fmt.Fprintf(&b, "🎤 Artists: %s\n", formatCount(user.ArtistCount))
	fmt.Fprintf(&b, "💿 Albums: %s\n", formatCount(user.AlbumCount))
	fmt.Fprintf(&b, "🎵 Tracks: %s\n", formatCount(user.TrackCount))

	if track != nil {
		label := "Last played"
		if track.NowPlaying() {
			label = "Now playing"
		}
		fmt.Fprintf(&b, "\n▶️ %s: %s — %s\n", label, track.Artist.Name, track.Name)
	}

	return strings.TrimRight(b.String(), "\n")
}

// adds thousands separators to a numeric string from Last.fm.
func formatCount(value string) string {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return "0"
	}
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
