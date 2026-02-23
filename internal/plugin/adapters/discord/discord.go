// Package discord implements a Discord channel adapter.
package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/openclio/openclio/internal/plugin"
)

const discordMaxLen = 2000

// Adapter is the Discord channel adapter.
type Adapter struct {
	session  *discordgo.Session
	token    string
	appID    string
	logger   *slog.Logger
	done     chan struct{}
	mu       sync.Mutex
	outbound <-chan plugin.OutboundMessage
}

// New creates a new Discord adapter.
func New(token, appID string, logger *slog.Logger) (*Adapter, error) {
	if token == "" {
		return nil, fmt.Errorf("discord: bot token is required (set DISCORD_BOT_TOKEN)")
	}
	return &Adapter{
		token:  token,
		appID:  appID,
		logger: logger,
		done:   make(chan struct{}),
	}, nil
}

func (a *Adapter) Name() string { return "discord" }

func (a *Adapter) Start(ctx context.Context, inbound chan<- plugin.InboundMessage, outbound <-chan plugin.OutboundMessage) error {
	a.mu.Lock()
	a.outbound = outbound
	a.mu.Unlock()

	dg, err := discordgo.New("Bot " + a.token)
	if err != nil {
		return fmt.Errorf("discord: creating session: %w", err)
	}
	a.session = dg

	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages

	dg.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author.ID == s.State.User.ID {
			return
		}

		text := m.Content
		isDM := m.GuildID == ""
		isMentioned := strings.Contains(text, "<@"+s.State.User.ID+">")

		if !isDM && !isMentioned {
			return
		}

		text = strings.ReplaceAll(text, "<@"+s.State.User.ID+">", "")
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}

		s.ChannelTyping(m.ChannelID)

		select {
		case inbound <- plugin.InboundMessage{
			AdapterName: a.Name(),
			UserID:      m.Author.ID,
			ChatID:      m.ChannelID,
			Text:        text,
		}:
		case <-ctx.Done():
		}
	})

	if a.appID != "" {
		dg.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			if i.ApplicationCommandData().Name == "chat" {
				opts := i.ApplicationCommandData().Options
				if len(opts) == 0 {
					return
				}
				text := opts[0].StringValue()
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
				})
				select {
				case inbound <- plugin.InboundMessage{
					AdapterName: a.Name(),
					UserID:      i.Member.User.ID,
					ChatID:      i.ChannelID,
					Text:        text,
				}:
				case <-ctx.Done():
				}
			}
		})
	}

	if err := dg.Open(); err != nil {
		return fmt.Errorf("discord: opening connection: %w", err)
	}
	defer dg.Close()

	if a.appID != "" {
		_, err := dg.ApplicationCommandCreate(a.appID, "", &discordgo.ApplicationCommand{
			Name:        "chat",
			Description: "Chat with your AI agent",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "message",
					Description: "Your message",
					Required:    true,
				},
			},
		})
		if err != nil {
			a.logger.Warn("discord: failed to register slash command", "error", err)
		}
	}

	a.logger.Info("discord adapter started", "user", dg.State.User.Username)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.done:
				return
			case resp := <-outbound:
				for _, chunk := range splitMessage(resp.Text, discordMaxLen) {
					if _, err := dg.ChannelMessageSend(resp.ChatID, chunk); err != nil {
						a.logger.Error("discord: send error", "error", err)
					}
				}
			}
		}
	}()

	select {
	case <-ctx.Done():
	case <-a.done:
	}
	return nil
}

func (a *Adapter) Stop() {
	select {
	case <-a.done:
	default:
		close(a.done)
	}
	if a.session != nil {
		a.session.Close()
	}
}

func (a *Adapter) Health() error {
	if a.session == nil {
		return fmt.Errorf("discord session not initialized")
	}
	// heartbeat latency is a good indicator of connection health
	if a.session.HeartbeatLatency() == 0 {
		return fmt.Errorf("discord session not tracking latency")
	}
	return nil
}

func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}
		cutAt := maxLen
		for i := maxLen; i > maxLen-300 && i > 0; i-- {
			if text[i] == '\n' {
				cutAt = i
				break
			}
		}
		chunks = append(chunks, text[:cutAt])
		text = text[cutAt:]
	}
	return chunks
}
