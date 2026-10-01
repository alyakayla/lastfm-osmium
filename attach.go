package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/ofabiodev/osmose/types"
)

func (b *bot) handleAttach(ctx context.Context, message *types.Message, args []string) error {
	if len(args) != 1 {
		_, err := message.Reply(ctx, "Usage: .attach <last.fm username>")
		return err
	}
	username := args[0]

	// Confirm the account exists and stores lastfm's canonical spelling.
	user, err := b.lastfm.UserInfo(ctx, username)
	if errors.Is(err, errUserNotFound) {
		_, err := message.Reply(ctx, fmt.Sprintf("No Last.fm user named %q.", username))
		return err
	}
	if err != nil {
		log.Printf("attach %s: %v", username, err)
		_, err := message.Reply(ctx, "Couldn't reach Last.fm right now, try again later.")
		return err
	}

	if err := b.store.SetLastfmUsername(ctx, message.AuthorID, user.Name); err != nil {
		log.Printf("attach %s: save link for %d: %v", username, message.AuthorID, err)
		_, err := message.Reply(ctx, "Something went wrong saving your account, try again later.")
		return err
	}

	_, err = message.Reply(ctx, fmt.Sprintf("Linked your Last.fm account %s. Run .bio to see your profile.", user.Name))
	return err
}
