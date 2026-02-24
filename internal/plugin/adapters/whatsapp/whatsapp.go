// Package whatsapp implements the WhatsApp channel adapter using the
// whatsmeow library (https://go.mau.fi/whatsmeow).
//
// # Setup
//
// 1. Set channels.whatsapp.enabled: true in config.yaml
// 2. No API token is required in current QR-login mode
// 3. Run `openclio serve` — a QR code will be printed to the terminal on first run
// 4. Scan with WhatsApp on your phone (Linked Devices → Link a Device)
// 5. Session is persisted in `~/.openclio/whatsapp.db` — subsequent starts auto-reconnect
package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/openclio/openclio/internal/plugin"

	_ "modernc.org/sqlite" // pure-Go SQLite driver for whatsmeow store
)

// Adapter is a WhatsApp channel adapter backed by whatsmeow.
type Adapter struct {
	client   *whatsmeow.Client
	db       *sql.DB
	dataDir  string
	logger   *slog.Logger
	inbound  chan<- plugin.InboundMessage
	outbound <-chan plugin.OutboundMessage
	done     chan struct{}
	qrMu     sync.RWMutex
	qrState  plugin.QRCodeState
	logState *whatsAppLogState
}

var nonDigitPattern = regexp.MustCompile(`[^0-9]+`)

// New creates a new WhatsApp adapter.
// dataDir: directory where the whatsapp.db session file is stored (e.g. ~/.openclio)
func New(dataDir string, logger *slog.Logger) (*Adapter, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("whatsapp: create data dir: %w", err)
	}

	waLogger, waState := newWhatsAppLogger(logger)
	dbFile := filepath.Join(dataDir, "whatsapp.db")
	openClient := func() (*sql.DB, *whatsmeow.Client, error) {
		dsn := (&url.URL{
			Scheme: "file",
			Path:   filepath.ToSlash(dbFile),
			RawQuery: url.Values{
				"_pragma": []string{
					"foreign_keys(1)",
					"journal_mode(WAL)",
					"busy_timeout(15000)",
					"synchronous(NORMAL)",
				},
			}.Encode(),
		}).String()
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, nil, fmt.Errorf("whatsapp: open sql db: %w", err)
		}
		// Keep a single SQLite connection for the WhatsApp session store. This avoids
		// internal writer contention that shows up as frequent SQLITE_BUSY errors.
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)

		container := sqlstore.NewWithDB(db, "sqlite", waLogger)
		if err := container.Upgrade(context.Background()); err != nil {
			_ = db.Close()
			return nil, nil, fmt.Errorf("whatsapp: open session store: %w", err)
		}

		deviceStore, err := container.GetFirstDevice(context.Background())
		if err != nil {
			_ = db.Close()
			return nil, nil, fmt.Errorf("whatsapp: get device store: %w", err)
		}

		client := whatsmeow.NewClient(deviceStore, waLogger)
		return db, client, nil
	}

	db, client, err := openClient()
	if err != nil {
		if shouldResetWhatsAppSessionStore(err) {
			if logger != nil {
				logger.Warn("whatsapp session store is unreadable; resetting local session and retrying", "error", err)
			}
			if resetErr := ResetStoredSession(dataDir); resetErr != nil {
				return nil, fmt.Errorf("whatsapp: reset corrupted session store failed: %w", resetErr)
			}
			db, client, err = openClient()
		}
	}
	if err != nil {
		return nil, err
	}

	return &Adapter{
		client:   client,
		db:       db,
		dataDir:  dataDir,
		logger:   logger,
		done:     make(chan struct{}),
		logState: waState,
	}, nil
}

// Name returns "whatsapp".
func (a *Adapter) Name() string { return "whatsapp" }

// Health returns nil if the client is connected to WhatsApp.
func (a *Adapter) Health() error {
	if a.client == nil {
		return fmt.Errorf("whatsapp adapter: client not initialised")
	}
	state := a.QRCodeState()
	switch strings.ToLower(strings.TrimSpace(state.Event)) {
	case "waiting_for_qr", "code":
		return nil
	}
	if !a.client.IsConnected() {
		return fmt.Errorf("whatsapp adapter: client not connected")
	}
	return nil
}

// Stop disconnects the WhatsApp client.
func (a *Adapter) Stop() {
	select {
	case <-a.done:
	default:
		close(a.done)
	}
	a.setQRState("stopped", "")
	if a.client != nil {
		a.client.Disconnect()
	}
}

// Close releases adapter-owned resources. It is safe to call multiple times.
func (a *Adapter) Close() error {
	if a.db == nil {
		return nil
	}
	err := a.db.Close()
	a.db = nil
	if err != nil {
		return fmt.Errorf("whatsapp: close session db: %w", err)
	}
	return nil
}

// ResetSession unlinks/clears the current WhatsApp session so the next connect
// path requires QR pairing again.
func (a *Adapter) ResetSession(ctx context.Context) error {
	if a.client == nil || a.client.Store == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if a.client.IsConnected() {
		if err := a.client.Logout(ctx); err != nil {
			a.logWarn("whatsapp logout failed; forcing local session reset", "error", err)
			a.client.Disconnect()
			if delErr := a.client.Store.Delete(ctx); delErr != nil {
				return fmt.Errorf("whatsapp: logout failed (%v) and local reset failed: %w", err, delErr)
			}
		}
	} else if a.client.Store.ID != nil {
		if err := a.client.Store.Delete(ctx); err != nil {
			return fmt.Errorf("whatsapp: clear local session: %w", err)
		}
	}

	a.setQRState("session_reset", "")
	return nil
}

// ResetStoredSession removes persisted WhatsApp session files from disk.
// This is used for force relink flows when no live adapter instance is running.
func ResetStoredSession(dataDir string) error {
	if strings.TrimSpace(dataDir) == "" {
		return fmt.Errorf("whatsapp: data dir is required for session reset")
	}
	files := []string{
		filepath.Join(dataDir, "whatsapp.db"),
		filepath.Join(dataDir, "whatsapp.db-shm"),
		filepath.Join(dataDir, "whatsapp.db-wal"),
	}
	for _, file := range files {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("whatsapp: remove %s: %w", file, err)
		}
	}
	return nil
}

// Start connects the WhatsApp client, prints the QR code if not yet paired,
// then dispatches inbound messages and delivers outbound responses.
func (a *Adapter) Start(ctx context.Context, inbound chan<- plugin.InboundMessage, outbound <-chan plugin.OutboundMessage) error {
	a.inbound = inbound
	a.outbound = outbound

	// Register message event handler
	a.client.AddEventHandler(a.handleEvent)

	// Connect — pair if needed
	if a.client.Store.ID == nil {
		a.setQRState("waiting_for_qr", "")
		// New login — print QR code
		qrChan, _ := a.client.GetQRChannel(ctx)
		if err := a.client.Connect(); err != nil {
			a.setQRState("connect_error", "")
			return fmt.Errorf("whatsapp: connect for QR: %w", err)
		}
		a.logInfo("WhatsApp QR code — scan with your phone:")
		for evt := range qrChan {
			if evt.Event == "code" {
				a.setQRState("code", evt.Code)
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else if strings.EqualFold(evt.Event, "success") {
				// Mark connected as soon as the QR login succeeds so the web UI can
				// confirm instantly without waiting for the channel to fully close.
				a.setQRState("connected", "")
				a.logInfo("WhatsApp QR link confirmed")
			} else {
				a.setQRState(evt.Event, "")
				a.logInfo("QR channel event", "event", evt.Event)
			}
		}
	} else {
		// Re-use existing session
		if err := a.client.Connect(); err != nil {
			a.setQRState("reconnect_error", "")
			return fmt.Errorf("whatsapp: reconnect: %w", err)
		}
	}

	a.setQRState("connected", "")
	a.logInfo("WhatsApp adapter connected", "jid", a.client.Store.ID)
	a.primeAppStateSync(ctx)

	// Outbound delivery loop
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.done:
				return
			case msg := <-outbound:
				if err := a.sendMessage(ctx, msg.ChatID, msg.Text); err != nil {
					a.logError("send message failed", "error", err)
				}
			}
		}
	}()

	// Block until context or stop
	select {
	case <-ctx.Done():
	case <-a.done:
	}

	a.client.Disconnect()
	return nil
}

// SendDirect sends a WhatsApp text message synchronously and returns delivery
// acceptance errors directly to the caller.
func (a *Adapter) SendDirect(ctx context.Context, chatID, text string) error {
	return a.sendMessage(ctx, chatID, text)
}

// QRCodeState returns the latest WhatsApp QR pairing state for web clients.
func (a *Adapter) QRCodeState() plugin.QRCodeState {
	a.qrMu.RLock()
	defer a.qrMu.RUnlock()
	return a.qrState
}

func (a *Adapter) setQRState(event, code string) {
	a.qrMu.Lock()
	defer a.qrMu.Unlock()
	a.qrState = plugin.QRCodeState{
		Event:     event,
		Code:      code,
		UpdatedAt: time.Now().UTC(),
	}
}

func (a *Adapter) sendMessage(ctx context.Context, rawChatID, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("message text is required")
	}
	if a.client == nil {
		return fmt.Errorf("whatsapp client is not initialized")
	}
	if !a.client.IsConnected() {
		return fmt.Errorf("whatsapp is not connected")
	}
	jid, err := normalizeWhatsAppChatID(rawChatID)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		msgID := a.client.GenerateMessageID()
		_, err := a.client.SendMessage(
			ctx,
			jid,
			&waProto.Message{Conversation: proto.String(text)},
			whatsmeow.SendRequestExtra{ID: msgID},
		)
		failCount, failLine := a.logState.consumeEncryptionFailure(string(msgID))
		if err == nil && failCount == 0 {
			return nil
		}
		if err == nil && failCount > 0 {
			err = fmt.Errorf("message encryption failed for %d recipient device(s): %s", failCount, failLine)
		}
		if err != nil {
			lastErr = err
		}
		if ctx.Err() != nil {
			return fmt.Errorf("whatsapp send failed: %w", ctx.Err())
		}
		if attempt < 2 && shouldRetryWhatsAppDelivery(lastErr) {
			time.Sleep(400 * time.Millisecond)
			continue
		}
		break
	}
	if lastErr != nil {
		return fmt.Errorf("whatsapp send failed: %w", lastErr)
	}
	return fmt.Errorf("whatsapp send failed")
}

func shouldRetryWhatsAppDelivery(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "no signal session established") ||
		strings.Contains(lower, "not connected") ||
		strings.Contains(lower, "timed out")
}

func shouldResetWhatsAppSessionStore(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "failed to check if foreign keys are enabled") ||
		(strings.Contains(lower, "sql logic error") && strings.Contains(lower, "out of memory"))
}

func normalizeWhatsAppChatID(raw string) (types.JID, error) {
	chatID := strings.TrimSpace(raw)
	if chatID == "" {
		return types.JID{}, fmt.Errorf("chat_id is required")
	}

	// Accept legacy c.us and map to whatsmeow's s.whatsapp.net user server.
	chatID = strings.ReplaceAll(chatID, "@c.us", "@s.whatsapp.net")

	if strings.Contains(chatID, "@") {
		jid, err := types.ParseJID(chatID)
		if err != nil {
			return types.JID{}, fmt.Errorf("invalid whatsapp chat_id %q: %w", raw, err)
		}
		if jid.Server == "" {
			return types.JID{}, fmt.Errorf("invalid whatsapp chat_id %q: missing server", raw)
		}
		return jid, nil
	}

	digits := nonDigitPattern.ReplaceAllString(chatID, "")
	if strings.HasPrefix(chatID, "+") {
		chatID = strings.TrimPrefix(chatID, "+")
		digits = nonDigitPattern.ReplaceAllString(chatID, "")
	}
	if strings.HasPrefix(digits, "00") {
		digits = strings.TrimPrefix(digits, "00")
	}
	if digits == "" {
		return types.JID{}, fmt.Errorf("invalid whatsapp phone number %q", raw)
	}
	// E.164 with country code is required when using phone numbers directly.
	if len(digits) < 11 {
		return types.JID{}, fmt.Errorf("whatsapp number %q is missing country code; use E.164 (example: 919500080653) or full JID (example: 919500080653@s.whatsapp.net)", raw)
	}

	jid, err := types.ParseJID(digits + "@s.whatsapp.net")
	if err != nil {
		return types.JID{}, fmt.Errorf("invalid whatsapp number %q: %w", raw, err)
	}
	return jid, nil
}

func (a *Adapter) primeAppStateSync(ctx context.Context) {
	if a.client == nil {
		return
	}
	go func() {
		patches := []appstate.WAPatchName{
			appstate.WAPatchCriticalBlock,
			appstate.WAPatchCriticalUnblockLow,
			appstate.WAPatchRegularHigh,
			appstate.WAPatchRegular,
			appstate.WAPatchRegularLow,
		}
		for _, name := range patches {
			select {
			case <-ctx.Done():
				return
			case <-a.done:
				return
			default:
			}
			fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := a.client.FetchAppState(fetchCtx, name, false, false)
			cancel()
			if err == nil {
				continue
			}
			if strings.Contains(strings.ToLower(err.Error()), "didn't find app state key") {
				a.logInfo("WhatsApp app state key is syncing", "patch", string(name))
				continue
			}
			a.logError("WhatsApp app state sync failed", "patch", string(name), "error", err)
		}
	}()
}

// handleEvent processes incoming WhatsApp events.
func (a *Adapter) handleEvent(rawEvt interface{}) {
	switch evt := rawEvt.(type) {
	case *events.Message:
		// Skip messages from self
		if evt.Info.IsFromMe {
			return
		}
		text := ""
		if m := evt.Message.GetConversation(); m != "" {
			text = m
		} else if m := evt.Message.GetExtendedTextMessage(); m != nil {
			text = m.GetText()
		}
		if text == "" {
			return // media / unsupported message type
		}

		chatID := evt.Info.Chat.String()
		userID := evt.Info.Sender.String()

		if a.inbound == nil {
			return
		}
		select {
		case <-a.done:
			return
		case a.inbound <- plugin.InboundMessage{
			AdapterName: a.Name(),
			UserID:      userID,
			ChatID:      chatID,
			Text:        text,
		}:
		}
	}
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

func (a *Adapter) logWarn(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Warn(msg, args...)
	}
}
