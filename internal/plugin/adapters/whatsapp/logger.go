package whatsapp

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	waLog "go.mau.fi/whatsmeow/util/log"
)

type whatsAppLogState struct {
	benignSocketCloseOnce sync.Once
	appStateKeyOnce       sync.Once
	mu                    sync.Mutex
	encryptFailures       map[string]encryptFailure
}

type encryptFailure struct {
	count int
	last  string
}

type whatsAppLogger struct {
	base   *slog.Logger
	module string
	state  *whatsAppLogState
}

func newWhatsAppLogger(base *slog.Logger) (waLog.Logger, *whatsAppLogState) {
	state := &whatsAppLogState{
		encryptFailures: make(map[string]encryptFailure),
	}
	if base == nil {
		return waLog.Stdout("openclio/whatsapp", "WARN", true), state
	}
	return &whatsAppLogger{
		base:   base,
		module: "openclio/whatsapp",
		state:  state,
	}, state
}

func (l *whatsAppLogger) Warnf(msg string, args ...interface{}) {
	l.output(slog.LevelWarn, msg, args...)
}

func (l *whatsAppLogger) Errorf(msg string, args ...interface{}) {
	l.output(slog.LevelError, msg, args...)
}

func (l *whatsAppLogger) Infof(msg string, args ...interface{}) {
	l.output(slog.LevelInfo, msg, args...)
}

func (l *whatsAppLogger) Debugf(msg string, args ...interface{}) {
	l.output(slog.LevelDebug, msg, args...)
}

func (l *whatsAppLogger) Sub(module string) waLog.Logger {
	module = strings.TrimSpace(module)
	if module == "" {
		return l
	}
	return &whatsAppLogger{
		base:   l.base,
		module: l.module + "/" + module,
		state:  l.state,
	}
}

func (l *whatsAppLogger) output(level slog.Level, msg string, args ...interface{}) {
	if l.base == nil {
		return
	}
	formatted := fmt.Sprintf(msg, args...)
	lower := strings.ToLower(formatted)

	if strings.Contains(lower, "error sending close to websocket") && strings.Contains(lower, "failed to read frame header: eof") {
		l.state.benignSocketCloseOnce.Do(func() {
			l.base.Debug("whatsapp websocket closed by remote peer", "component", l.module)
		})
		return
	}

	if strings.Contains(lower, "didn't find app state key") {
		l.state.appStateKeyOnce.Do(func() {
			l.base.Warn(
				"whatsapp app-state keys are still syncing; if this repeats, reconnect whatsapp to regenerate session keys",
				"component", l.module,
			)
		})
		l.base.Debug("whatsapp app-state sync detail", "component", l.module, "detail", formatted)
		return
	}

	if strings.HasPrefix(formatted, "Failed to encrypt ") {
		l.state.recordEncryptionFailure(formatted)
	}

	switch level {
	case slog.LevelDebug:
		l.base.Debug(formatted, "component", l.module)
	case slog.LevelInfo:
		l.base.Info(formatted, "component", l.module)
	case slog.LevelWarn:
		l.base.Warn(formatted, "component", l.module)
	default:
		l.base.Error(formatted, "component", l.module)
	}
}

func (s *whatsAppLogState) recordEncryptionFailure(line string) {
	if s == nil {
		return
	}
	rest := strings.TrimPrefix(line, "Failed to encrypt ")
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return
	}
	id := strings.TrimSpace(fields[0])
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.encryptFailures[id]
	cur.count++
	cur.last = line
	s.encryptFailures[id] = cur
}

func (s *whatsAppLogState) consumeEncryptionFailure(id string) (count int, last string) {
	if s == nil || strings.TrimSpace(id) == "" {
		return 0, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.encryptFailures[id]
	if !ok {
		return 0, ""
	}
	delete(s.encryptFailures, id)
	return cur.count, cur.last
}
