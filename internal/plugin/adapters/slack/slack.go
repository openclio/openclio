// Package slack implements a Slack channel adapter using the slack-go library.
package slack

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/slack-go/slack"

	"github.com/openclio/openclio/internal/plugin"
)

// Adapter is the Slack channel adapter.
type Adapter struct {
	client *slack.Client
	rtm    *slack.RTM
	token  string
	logger *slog.Logger
	done   chan struct{}
}

// New creates a new Slack adapter.
// token is the bot token from Slack (xoxb-...).
func New(token string, logger *slog.Logger) (*Adapter, error) {
	if token == "" {
		return nil, fmt.Errorf("slack: bot token is required (set SLACK_BOT_TOKEN)")
	}
	return &Adapter{
		client: slack.New(token),
		token:  token,
		logger: logger,
		done:   make(chan struct{}),
	}, nil
}

func (a *Adapter) Name() string { return "slack" }

func (a *Adapter) Start(ctx context.Context, inbound chan<- plugin.InboundMessage, outbound <-chan plugin.OutboundMessage) error {
	a.rtm = a.client.NewRTM()
	go a.rtm.ManageConnection()

	// Outbound delivery goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.done:
				return
			case msg := <-outbound:
				a.rtm.SendMessage(a.rtm.NewOutgoingMessage(msg.Text, msg.ChatID))
			}
		}
	}()

	a.logInfo("slack adapter started")

	// Event loop
	for {
		select {
		case <-ctx.Done():
			a.rtm.Disconnect()
			return nil
		case <-a.done:
			a.rtm.Disconnect()
			return nil
		case evt := <-a.rtm.IncomingEvents:
			switch ev := evt.Data.(type) {
			case *slack.MessageEvent:
				// Skip bot messages
				if ev.SubType == "bot_message" || ev.BotID != "" {
					continue
				}
				// Skip empty messages
				if ev.Text == "" {
					continue
				}
				select {
				case inbound <- plugin.InboundMessage{
					AdapterName: a.Name(),
					UserID:      ev.User,
					ChatID:      ev.Channel,
					Text:        ev.Text,
				}:
				case <-ctx.Done():
					return nil
				case <-a.done:
					return nil
				}
			case *slack.RTMError:
				a.logError("slack RTM error", "error", ev.Error())
			case *slack.InvalidAuthEvent:
				return fmt.Errorf("slack: invalid authentication token")
			}
		}
	}
}

func (a *Adapter) Stop() {
	select {
	case <-a.done:
	default:
		close(a.done)
	}
	if a.rtm != nil {
		a.rtm.Disconnect()
	}
}

// Health checks that the Slack token is valid by calling the auth.test API.
func (a *Adapter) Health() error {
	_, err := a.client.AuthTest()
	if err != nil {
		return fmt.Errorf("slack: auth check failed: %w", err)
	}
	return nil
}

func (a *Adapter) logInfo(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Info(msg, args...)
	}
}

func (a *Adapter) logError(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Error(msg, args...)
	}
}
