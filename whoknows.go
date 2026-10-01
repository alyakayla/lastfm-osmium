package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ofabiodev/osmose/types"
)

const (
	whoKnowsShown    = 15 // listeners listed in the reply
	whoKnowsRequests = 5  // concurrent Last.fm requests
	memberBatchSize  = 100
)

type listener struct {
	name  string
	plays int
}

// an attached account whose owner belongs to the current server.
type serverMember struct {
	link
	name string
}

// replies to ".w [artist]" with how many times each attached member of the
// current server has scrobbled an artist. Without an artist it uses the
// invoker's latest track.
func (b *bot) handleWhoKnows(ctx context.Context, message *types.Message, args []string) error {
	communityID := message.Chat.CommunityID
	if communityID == 0 {
		_, err := message.Reply(ctx, ".w only works in a server.")
		return err
	}

	artist := strings.Join(args, " ")
	if artist == "" {
		username, ok, err := b.linkedUsername(ctx, message, ".w <artist>")
		if !ok {
			return err
		}
		track, err := b.lastfm.LatestTrack(ctx, username)
		if err != nil {
			log.Printf("whoknows: latest track for %s: %v", username, err)
			_, err := message.Reply(ctx, "Couldn't reach Last.fm right now, try again later.")
			return err
		}
		if track == nil {
			_, err := message.Reply(ctx, "You haven't scrobbled anything yet. Try .w <artist>.")
			return err
		}
		artist = track.Artist.Name
	}

	links, err := b.store.Links(ctx)
	if err != nil {
		log.Printf("whoknows: list links: %v", err)
		_, err := message.Reply(ctx, "Something went wrong, try again later.")
		return err
	}

	members, err := b.serverMembers(ctx, communityID, links)
	if err != nil {
		_, err := message.Reply(ctx, "Couldn't load this server's members, try again later.")
		return err
	}
	if len(members) == 0 {
		_, err := message.Reply(ctx, "Nobody in this server has linked a Last.fm account yet. Use .attach <last.fm username>.")
		return err
	}

	artistName, listeners, err := b.artistListeners(ctx, artist, members)
	if errors.Is(err, errNotFound) {
		_, err := message.Reply(ctx, fmt.Sprintf("No artist named %q on Last.fm.", artist))
		return err
	}
	if err != nil {
		log.Printf("whoknows %s: %v", artist, err)
		_, err := message.Reply(ctx, "Couldn't reach Last.fm right now, try again later.")
		return err
	}

	_, err = message.Reply(ctx, formatWhoKnows(artistName, listeners))
	return err
}

// keeps the links whose owners are members of the community, named by their
// server nickname, then display name, then username.
func (b *bot) serverMembers(ctx context.Context, communityID types.ID, links []link) ([]serverMember, error) {
	byID := make(map[types.ID]link, len(links))
	ids := make([]types.ID, 0, len(links))
	for _, l := range links {
		byID[l.OsmiumID] = l
		ids = append(ids, l.OsmiumID)
	}

	manager := b.client.Members.In(communityID)
	var members []serverMember
	for start := 0; start < len(ids); start += memberBatchSize {
		batch := ids[start:min(start+memberBatchSize, len(ids))]
		found, err := manager.FetchMany(ctx, batch...)
		if err != nil {
			return nil, err
		}
		for _, m := range found {
			l, ok := byID[m.ID]
			if !ok || m.CommunityID != communityID {
				continue
			}
			members = append(members, serverMember{link: l, name: memberName(m, l)})
		}
	}
	return members, nil
}

func memberName(m *types.Member, l link) string {
	if m.Nickname != nil && *m.Nickname != "" {
		return *m.Nickname
	}
	if m.User != nil {
		if m.User.Name != "" {
			return m.User.Name
		}
		if m.User.Username != "" {
			return m.User.Username
		}
	}
	return l.LastfmUsername
}

// fetches every member's play count for artist and returns those who have
// listened, most plays first. Ties are broken by name so the crown goes to the
// alphabetically first listener.
func (b *bot) artistListeners(ctx context.Context, artist string, members []serverMember) (string, []listener, error) {
	type result struct {
		member serverMember
		name   string
		plays  int
		err    error
	}
	results := make([]result, len(members))
	sem := make(chan struct{}, whoKnowsRequests)
	var wg sync.WaitGroup
	for i, m := range members {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			name, plays, err := b.lastfm.ArtistPlays(ctx, artist, m.LastfmUsername)
			results[i] = result{member: m, name: name, plays: plays, err: err}
		}()
	}
	wg.Wait()

	artistName := ""
	var listeners []listener
	var firstErr error
	for _, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			log.Printf("whoknows %s: %s: %v", artist, r.member.LastfmUsername, r.err)
			continue
		}
		artistName = r.name
		if r.plays > 0 {
			listeners = append(listeners, listener{name: r.member.name, plays: r.plays})
		}
	}
	// Only fail when no lookup succeeded; one broken account shouldn't hide the rest.
	if artistName == "" {
		return "", nil, firstErr
	}

	sortListeners(listeners)
	return artistName, listeners, nil
}

// orders by plays, most first, breaking ties alphabetically so the crown goes
// to the first name.
func sortListeners(listeners []listener) {
	sort.Slice(listeners, func(i, j int) bool {
		if listeners[i].plays != listeners[j].plays {
			return listeners[i].plays > listeners[j].plays
		}
		return strings.ToLower(listeners[i].name) < strings.ToLower(listeners[j].name)
	})
}

func formatWhoKnows(artist string, listeners []listener) string {
	if len(listeners) == 0 {
		return fmt.Sprintf("Nobody here has listened to %s yet.", artist)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Who knows %s?\n\n", artist)

	total := 0
	for i, l := range listeners {
		total += l.plays
		if i >= whoKnowsShown {
			continue
		}
		prefix := strconv.Itoa(i+1) + "."
		if i == 0 {
			prefix = "👑"
		}
		fmt.Fprintf(&b, "%s %s ~ %s %s\n", prefix, l.name, formatCount(strconv.Itoa(l.plays)), plural(l.plays, "play", "plays"))
	}
	if hidden := len(listeners) - whoKnowsShown; hidden > 0 {
		fmt.Fprintf(&b, "…and %d more\n", hidden)
	}

	fmt.Fprintf(&b, "\n%d %s · %s total %s", len(listeners), plural(len(listeners), "listener", "listeners"),
		formatCount(strconv.Itoa(total)), plural(total, "play", "plays"))
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
