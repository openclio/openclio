// Package telegram implements a Telegram bot channel adapter.
package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	tele "gopkg.in/telebot.v3"

	"github.com/openclio/openclio/internal/plugin"
)

// Adapter is the Telegram channel adapter.
type Adapter struct {
	bot    *tele.Bot
	logger *slog.Logger
	token  string
	done   chan struct{}
}

// New creates a new Telegram adapter.
// token is the bot token from @BotFather.
func New(token string, logger *slog.Logger) (*Adapter, error) {
	if token == "" {
		return nil, fmt.Errorf("telegram: bot token is required (set TELEGRAM_BOT_TOKEN)")
	}
	return &Adapter{
		token:  token,
		logger: logger,
		done:   make(chan struct{}),
	}, nil
}

func (a *Adapter) Name() string { return "telegram" }

func (a *Adapter) Start(ctx context.Context, inbound chan<- plugin.InboundMessage, outbound <-chan plugin.OutboundMessage) error {
	pref := tele.Settings{
		Token:  a.token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}
	bot, err := tele.NewBot(pref)
	if err != nil {
		return fmt.Errorf("telegram: creating bot: %w", err)
	}
	a.bot = bot

	// Handle all text messages
	bot.Handle(tele.OnText, func(c tele.Context) error {
		msg := plugin.InboundMessage{
			AdapterName: a.Name(),
			UserID:      fmt.Sprintf("%d", c.Sender().ID),
			ChatID:      fmt.Sprintf("%d", c.Chat().ID),
			Text:        c.Text(),
		}
		// Send typing action while agent processes
		c.Notify(tele.Typing)

		select {
		case inbound <- msg:
		case <-ctx.Done():
		}
		return nil
	})

	// Handle /start command
	bot.Handle("/start", func(c tele.Context) error {
		return c.Send("👋 Hi! I'm your personal AI agent. Send me a message to get started.")
	})

	// Handle /help command
	bot.Handle("/help", func(c tele.Context) error {
		return c.Send("Just type any message to chat with me!\n\nCommands:\n/start - Welcome\n/help - Show this message")
	})

	// Response sender goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.done:
				return
			case resp := <-outbound:
				chatID, err := parseChatID(resp.ChatID)
				if err != nil {
					a.logger.Error("telegram: invalid chat ID", "chatID", resp.ChatID, "error", err)
					continue
				}
				chat := &tele.Chat{ID: chatID}
				// Split long messages (Telegram has 4096 char limit)
				for _, chunk := range splitMessage(resp.Text, 4096) {
					if _, err := bot.Send(chat, chunk); err != nil {
						a.logger.Error("telegram: send error", "error", err)
					}
				}
			}
		}
	}()

	a.logger.Info("telegram adapter started", "bot", bot.Me.Username)

	// Start polling — blocks until stopped
	go bot.Start()

	// Wait for context cancellation
	<-ctx.Done()
	bot.Stop()
	return nil
}

func (a *Adapter) Stop() {
	select {
	case <-a.done:
	default:
		close(a.done)
	}
	if a.bot != nil {
		a.bot.Stop()
	}
}

func (a *Adapter) Health() error {
	// A basic health check: can we hit the Telegram getMe endpoint?
	resp, err := http.Get(fmt.Sprintf("%s/bot%s/getMe", "https://api.telegram.org", a.token))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram api returned status: %d", resp.StatusCode)
	}
	return nil
}

func parseChatID(s string) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(s, "%d", &id)
	return id, err
}

// splitMessage splits text into chunks of at most maxLen characters.
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
		// Try to split at newline
		cutAt := maxLen
		for i := maxLen; i > maxLen-200 && i > 0; i-- {
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
