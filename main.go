package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"

	"github.com/joho/godotenv"
	"github.com/ofabiodev/osmose"
	"github.com/ofabiodev/osmose/types"
)

type bot struct {
	client *osmose.Client
	lastfm *lastfmClient
	store  *store
}

// returns the Last.fm username the message author linked with
// .attach. When ok is false it has already replied explaining why, and err is
// the result of that reply. alternative is suggested as another way to run
// the command.
func (b *bot) linkedUsername(ctx context.Context, message *types.Message, alternative string) (username string, ok bool, err error) {
	linked, err := b.store.LastfmUsername(ctx, message.AuthorID)
	if err != nil {
		log.Printf("lookup linked account for %d: %v", message.AuthorID, err)
		_, err := message.Reply(ctx, "Something went wrong, try again later.")
		return "", false, err
	}
	if linked == "" {
		_, err := message.Reply(ctx, "You haven't linked a Last.fm account yet. Use .attach <last.fm username> or "+alternative+".")
		return "", false, err
	}
	return linked, true, nil
}

func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatal(err)
	}

	apiKey := os.Getenv("LASTFM_API_KEY")
	if apiKey == "" {
		log.Fatal("LASTFM_API_KEY is not set")
	}

	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "lastfm-osmium.db"
	}
	db, err := openStore(dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client, err := osmose.New(osmose.Config{
		Token:    os.Getenv("OSMIUM_TOKEN"),
		ClientID: 772290,
	})
	if err != nil {
		log.Fatal(err)
	}

	b := &bot{client: client, lastfm: newLastfmClient(apiKey), store: db}

	client.OnReady(func(_ context.Context, event *osmose.ReadyEvent) error {
		log.Printf("connected as %s", event.User.Username)
		return nil
	})

	client.OnMessage(func(ctx context.Context, message *types.Message) error {
		// in case a bot account invoked the command.
		if message.Author != nil && message.Author.Bot {
			return nil
		}
		if self := client.User(); self != nil && message.AuthorID == self.ID {
			return nil
		}

		fields := strings.Fields(message.Content)
		if len(fields) == 0 {
			return nil
		}
		switch fields[0] {
		case ".bio":
			return b.handleBio(ctx, message, fields[1:])
		case ".attach":
			return b.handleAttach(ctx, message, fields[1:])
		case ".c", ".chart":
			return b.handleChart(ctx, message, fields[1:])
		case ".w", ".whoknows":
			return b.handleWhoKnows(ctx, message, fields[1:])
		}
		return nil
	})

	if err := client.Run(ctx); err != nil {
		db.Close()
		log.Fatal(err)
	}
}
