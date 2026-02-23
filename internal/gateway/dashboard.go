package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openclio/openclio/internal/config"
	agentcron "github.com/openclio/openclio/internal/cron"
	"github.com/openclio/openclio/internal/logger"
	"github.com/openclio/openclio/internal/plugin"
	"github.com/openclio/openclio/internal/privacy"
	"github.com/openclio/openclio/internal/storage"
	"github.com/openclio/openclio/internal/workspace"
	"rsc.io/qr"
)

// Overview returns high-level dashboard summary data.
func (h *Handlers) Overview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	setupRequired, setupReason := h.setupState()

	sessionCount := 0
	if h.sessions != nil {
		if n, err := h.sessions.Count(); err == nil {
			sessionCount = n
		}
	}

	callCount := 0
	inputTokens := 0
	outputTokens := 0
	totalCost := float64(0)
	if h.costTracker != nil {
		if s, err := h.costTracker.GetSummary("all"); err == nil {
			callCount = s.CallCount
			inputTokens = s.InputTokens
			outputTokens = s.OutputTokens
			totalCost = s.TotalCost
		}
	}

	channelsCount := 0
	healthyCount := 0
	if h.manager != nil {
		statuses := h.manager.Statuses()
		channelsCount = len(statuses)
		for _, st := range statuses {
			if st.Healthy {
				healthyCount++
			}
		}
	}

	cronJobsCount := 0
	if h.scheduler != nil {
		cronJobsCount = len(h.scheduler.ListJobs())
	}

	skillsCount := 0
	if skills, err := workspace.ListSkills(h.dataDir); err == nil {
		skillsCount = len(skills)
	}

	provider := ""
	model := ""
	name := ""
	baseURL := ""
	if h.cfg != nil {
		provider = h.cfg.Model.Provider
		model = h.cfg.Model.Model
		name = h.cfg.Model.Name
		baseURL = h.cfg.Model.BaseURL
	}

	uptime := int64(0)
	if !h.startedAt.IsZero() {
		uptime = int64(time.Since(h.startedAt).Seconds())
	}

	privacySummary := map[string]any{
		"scrub_output":     h.cfg != nil && h.cfg.Tools.ScrubOutput,
		"secrets_redacted": int64(0),
	}
	if report, err := privacy.BuildReport(h.costTracker, h.privacyStore, h.cfg != nil && h.cfg.Tools.ScrubOutput, "all"); err == nil && report != nil {
		privacySummary["secrets_redacted"] = report.Privacy.SecretsRedacted
	}

	embeddingSummary := map[string]any{
		"error_count":   int64(0),
		"unique_errors": int64(0),
		"last_error_at": "",
	}
	if h.embeddingErrs != nil {
		if s, err := h.embeddingErrs.Summary(); err == nil {
			embeddingSummary["error_count"] = s.TotalCount
			embeddingSummary["unique_errors"] = s.UniqueCount
			if !s.LastSeen.IsZero() {
				embeddingSummary["last_error_at"] = s.LastSeen.UTC().Format(time.RFC3339)
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"time":           time.Now().UTC().Format(time.RFC3339),
		"version":        "dev",
		"setup_required": setupRequired,
		"setup_reason":   setupReason,
		"uptime_seconds": uptime,
		"model": map[string]any{
			"provider": provider,
			"model":    model,
			"name":     name,
			"base_url": baseURL,
		},
		"usage": map[string]any{
			"llm_calls":      callCount,
			"input_tokens":   inputTokens,
			"output_tokens":  outputTokens,
			"estimated_cost": totalCost,
		},
		"counts": map[string]any{
			"sessions":         sessionCount,
			"channels":         channelsCount,
			"channels_healthy": healthyCount,
			"cron_jobs":        cronJobsCount,
			"skills":           skillsCount,
		},
		"privacy":    privacySummary,
		"embeddings": embeddingSummary,
	})
}

// Channels returns configured and runtime channel adapter status.
func (h *Handlers) Channels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	type channelRow struct {
		Name                string `json:"name"`
		Configured          bool   `json:"configured"`
		Running             bool   `json:"running"`
		Healthy             bool   `json:"healthy"`
		LastHealthCheck     string `json:"last_health_check,omitempty"`
		LastHealthError     string `json:"last_health_error,omitempty"`
		RestartCount        int    `json:"restart_count"`
		ConsecutiveFailures int    `json:"consecutive_failures"`
		LastStart           string `json:"last_start,omitempty"`
		LastError           string `json:"last_error,omitempty"`
	}

	configured := map[string]bool{
		"webchat": true, // always registered in serve mode
	}
	if h.cfg != nil {
		if h.cfg.Channels.Telegram != nil {
			configured["telegram"] = true
		}
		if h.cfg.Channels.Discord != nil {
			configured["discord"] = true
		}
		if h.cfg.Channels.WhatsApp != nil && h.cfg.Channels.WhatsApp.Enabled {
			configured["whatsapp"] = true
		}
		if h.cfg.Channels.Slack != nil {
			configured["slack"] = true
		}
	}

	byName := map[string]channelRow{}
	for name := range configured {
		byName[name] = channelRow{Name: name, Configured: true}
	}
	if h.manager != nil {
		for _, st := range h.manager.Statuses() {
			row := byName[st.Name]
			row.Name = st.Name
			row.Running = st.Running
			row.Healthy = st.Healthy
			row.RestartCount = st.RestartCount
			row.ConsecutiveFailures = st.ConsecutiveFailures
			row.LastHealthError = st.LastHealthError
			row.LastError = st.LastError
			if !st.LastHealthCheck.IsZero() {
				row.LastHealthCheck = st.LastHealthCheck.UTC().Format(time.RFC3339)
			}
			if !st.LastStart.IsZero() {
				row.LastStart = st.LastStart.UTC().Format(time.RFC3339)
			}
			if _, ok := configured[st.Name]; ok {
				row.Configured = true
			}
			byName[st.Name] = row
		}
	}

	rows := make([]channelRow, 0, len(byName))
	for _, row := range byName {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })

	allowAll := true
	approved := 0
	if h.allowlist != nil {
		allowAll = h.allowlist.AllowAll()
		approved = len(h.allowlist.List())
	} else if h.cfg != nil {
		allowAll = h.cfg.Channels.AllowAll
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"channels": rows,
		"allowlist": map[string]any{
			"allow_all":        allowAll,
			"approved_senders": approved,
		},
	})
}

// ChannelWhatsAppQR returns the latest WhatsApp QR pairing state for web clients.
func (h *Handlers) ChannelWhatsAppQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if h.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "channel manager is not configured")
		return
	}
	adapter := h.manager.AdapterByName("whatsapp")
	if adapter == nil {
		writeError(w, http.StatusNotFound, "whatsapp adapter is not available")
		return
	}

	qrProvider, ok := adapter.(interface {
		QRCodeState() plugin.QRCodeState
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "whatsapp adapter does not expose qr state")
		return
	}
	state := qrProvider.QRCodeState()
	trimmedCode := strings.TrimSpace(state.Code)
	available := strings.EqualFold(state.Event, "code") && trimmedCode != ""
	connected := strings.EqualFold(state.Event, "connected") || strings.EqualFold(state.Event, "success")
	message := "WhatsApp QR is not available yet."
	switch {
	case connected:
		message = "WhatsApp is connected to openclio."
	case available:
		message = "Scan this QR in WhatsApp Linked Devices to connect openclio."
	case state.Event != "":
		message = "WhatsApp pairing state: " + state.Event
	}

	updatedAt := ""
	if !state.UpdatedAt.IsZero() {
		updatedAt = state.UpdatedAt.UTC().Format(time.RFC3339)
	}
	qrImageDataURL := ""
	if available {
		qrImageDataURL = encodeQRCodePNGDataURL(trimmedCode)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":       "whatsapp",
		"brand":      "openclio",
		"available":  available,
		"connected":  connected,
		"event":      state.Event,
		"code":       trimmedCode,
		"qr_image":   qrImageDataURL,
		"updated_at": updatedAt,
		"message":    message,
	})
}

func encodeQRCodePNGDataURL(code string) string {
	text := strings.TrimSpace(code)
	if text == "" {
		return ""
	}
	qrCode, err := qr.Encode(text, qr.M)
	if err != nil {
		return ""
	}
	const modulePixels = 6
	const quietModules = 4
	sizePx := (qrCode.Size + (quietModules * 2)) * modulePixels
	if sizePx <= 0 {
		return ""
	}

	img := image.NewRGBA(image.Rect(0, 0, sizePx, sizePx))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	black := color.RGBA{R: 17, G: 24, B: 39, A: 255}

	for y := 0; y < qrCode.Size; y++ {
		for x := 0; x < qrCode.Size; x++ {
			if !qrCode.Black(x, y) {
				continue
			}
			startX := (x + quietModules) * modulePixels
			startY := (y + quietModules) * modulePixels
			for yy := 0; yy < modulePixels; yy++ {
				for xx := 0; xx < modulePixels; xx++ {
					img.Set(startX+xx, startY+yy, black)
				}
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// ChannelAllowlist returns the expanded list of approved sender identities.
func (h *Handlers) ChannelAllowlist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if h.allowlist == nil {
		writeError(w, http.StatusServiceUnavailable, "allowlist is not configured")
		return
	}
	raw := h.allowlist.List()
	sort.Strings(raw)

	entries := make([]map[string]string, 0, len(raw))
	for _, key := range raw {
		adapter := ""
		userID := key
		if idx := strings.Index(key, ":"); idx >= 0 {
			adapter = key[:idx]
			userID = key[idx+1:]
		}
		entries = append(entries, map[string]string{
			"adapter": adapter,
			"user_id": userID,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"allow_all": h.allowlist.AllowAll(),
		"count":     len(entries),
		"entries":   entries,
	})
}

type channelAllowlistIdentityPayload struct {
	Adapter string `json:"adapter"`
	UserID  string `json:"user_id"`
}

// ChannelAllowlistApprove approves one adapter/user identity for strict mode.
func (h *Handlers) ChannelAllowlistApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if h.allowlist == nil {
		writeError(w, http.StatusServiceUnavailable, "allowlist is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var payload channelAllowlistIdentityPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	adapter := strings.TrimSpace(strings.ToLower(payload.Adapter))
	userID := strings.TrimSpace(payload.UserID)
	if adapter == "" || userID == "" {
		writeError(w, http.StatusBadRequest, "adapter and user_id are required")
		return
	}
	if err := h.allowlist.Approve(adapter, userID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to approve sender: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"adapter": adapter,
		"user_id": userID,
	})
}

// ChannelAllowlistRevoke revokes one adapter/user identity.
func (h *Handlers) ChannelAllowlistRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if h.allowlist == nil {
		writeError(w, http.StatusServiceUnavailable, "allowlist is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var payload channelAllowlistIdentityPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	adapter := strings.TrimSpace(strings.ToLower(payload.Adapter))
	userID := strings.TrimSpace(payload.UserID)
	if adapter == "" || userID == "" {
		writeError(w, http.StatusBadRequest, "adapter and user_id are required")
		return
	}
	if err := h.allowlist.Revoke(adapter, userID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke sender: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"adapter": adapter,
		"user_id": userID,
	})
}

type channelAllowlistModePayload struct {
	AllowAll *bool `json:"allow_all"`
}

// ChannelAllowlistMode updates strict mode at runtime.
func (h *Handlers) ChannelAllowlistMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "use PUT")
		return
	}
	if h.allowlist == nil {
		writeError(w, http.StatusServiceUnavailable, "allowlist is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var payload channelAllowlistModePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if payload.AllowAll == nil {
		writeError(w, http.StatusBadRequest, "allow_all is required")
		return
	}
	h.allowlist.SetAllowAll(*payload.AllowAll)
	if h.cfg != nil {
		h.cfg.Channels.AllowAll = *payload.AllowAll
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"allow_all": *payload.AllowAll,
		"count":     len(h.allowlist.List()),
	})
}

type channelActionPayload struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

// ChannelAction provides baseline channel control hooks.
func (h *Handlers) ChannelAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if h.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "channel manager is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var payload channelActionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	name := strings.TrimSpace(strings.ToLower(payload.Name))
	action := strings.TrimSpace(strings.ToLower(payload.Action))
	if name == "" || action == "" {
		writeError(w, http.StatusBadRequest, "name and action are required")
		return
	}

	var found any
	for _, st := range h.manager.Statuses() {
		if st.Name == name {
			found = st
			break
		}
	}
	if found == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	switch action {
	case "ping", "refresh":
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"name":    name,
			"action":  action,
			"status":  found,
			"message": "status refreshed",
		})
	case "restart", "connect", "disconnect":
		writeError(w, http.StatusNotImplemented, "runtime channel lifecycle action is not yet implemented")
	default:
		writeError(w, http.StatusBadRequest, "action must be one of: ping|refresh|restart|connect|disconnect")
	}
}

// Instances returns local runtime process and gateway instance information.
func (h *Handlers) Instances(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	uptime := int64(0)
	if !h.startedAt.IsZero() {
		uptime = int64(time.Since(h.startedAt).Seconds())
	}

	bind := ""
	port := 0
	grpcPort := 0
	if h.cfg != nil {
		bind = h.cfg.Gateway.Bind
		port = h.cfg.Gateway.Port
		grpcPort = h.cfg.Gateway.GRPCPort
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"instances": []map[string]any{
			{
				"id":             "local",
				"kind":           "gateway",
				"pid":            os.Getpid(),
				"started_at":     h.startedAt.UTC().Format(time.RFC3339),
				"uptime_seconds": uptime,
				"goos":           runtime.GOOS,
				"goarch":         runtime.GOARCH,
				"go_version":     runtime.Version(),
				"bind":           bind,
				"port":           port,
				"grpc_port":      grpcPort,
				"data_dir":       h.dataDir,
			},
		},
	})
}

type instanceActionPayload struct {
	ID     string `json:"id,omitempty"`
	Action string `json:"action"`
}

// InstanceAction handles baseline runtime instance control actions.
func (h *Handlers) InstanceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var payload instanceActionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	action := strings.TrimSpace(strings.ToLower(payload.Action))
	if action == "" {
		writeError(w, http.StatusBadRequest, "action is required")
		return
	}

	switch action {
	case "ping":
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"action":   "ping",
			"response": "pong",
			"pid":      os.Getpid(),
			"time":     time.Now().UTC().Format(time.RFC3339),
		})
	case "refresh":
		runtime.GC()
		uptime := int64(0)
		if !h.startedAt.IsZero() {
			uptime = int64(time.Since(h.startedAt).Seconds())
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             true,
			"action":         "refresh",
			"uptime_seconds": uptime,
			"time":           time.Now().UTC().Format(time.RFC3339),
		})
	default:
		writeError(w, http.StatusBadRequest, "action must be one of: ping|refresh")
	}
}

// CronJobs returns registered cron jobs and computed next/last run info.
func (h *Handlers) CronJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if h.scheduler == nil {
		writeJSON(w, http.StatusOK, map[string]any{"jobs": []any{}})
		return
	}
	jobs := h.scheduler.ListJobs()
	type row struct {
		ID          int64  `json:"id,omitempty"`
		Name        string `json:"name"`
		Schedule    string `json:"schedule"`
		Channel     string `json:"channel,omitempty"`
		SessionMode string `json:"session_mode"`
		TimeoutSec  int    `json:"timeout_seconds"`
		Enabled     bool   `json:"enabled"`
		Source      string `json:"source"`
		LastRun     string `json:"last_run,omitempty"`
		NextRun     string `json:"next_run,omitempty"`
	}
	out := make([]row, 0, len(jobs))
	for _, j := range jobs {
		item := row{
			ID:          j.ID,
			Name:        j.Name,
			Schedule:    j.Schedule,
			Channel:     j.Channel,
			SessionMode: j.SessionMode,
			TimeoutSec:  j.TimeoutSec,
			Enabled:     j.Enabled,
			Source:      j.Source,
		}
		if !j.LastRun.IsZero() {
			item.LastRun = j.LastRun.UTC().Format(time.RFC3339)
		}
		if !j.NextRun.IsZero() {
			item.NextRun = j.NextRun.UTC().Format(time.RFC3339)
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

// CronHistory returns recent cron execution history.
func (h *Handlers) CronHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	limit := parseIntQuery(r, "limit", 20, 1, 200)
	if h.scheduler == nil {
		writeJSON(w, http.StatusOK, map[string]any{"history": []any{}})
		return
	}
	history, err := h.scheduler.History(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load cron history: "+err.Error())
		return
	}
	out := make([]map[string]any, 0, len(history))
	for _, e := range history {
		row := map[string]any{
			"job_name":    e.JobName,
			"duration_ms": e.DurationMs,
			"success":     e.Success,
			"output":      e.Output,
		}
		if !e.RanAt.IsZero() {
			row["ran_at"] = e.RanAt.UTC().Format(time.RFC3339)
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": out})
}

// CronRun triggers one named cron job immediately.
func (h *Handlers) CronRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if h.scheduler == nil {
		writeError(w, http.StatusServiceUnavailable, "cron scheduler is not configured")
		return
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := h.scheduler.RunNow(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": name})
}

type cronJobMutationPayload struct {
	Name        string `json:"name,omitempty"`
	Schedule    string `json:"schedule"`
	Trigger     string `json:"trigger,omitempty"`
	Prompt      string `json:"prompt"`
	Channel     string `json:"channel,omitempty"`
	SessionMode string `json:"session_mode,omitempty"`
	TimeoutSec  int    `json:"timeout_seconds,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

// CronJobCreate creates one DB-backed cron job and hot-registers it.
func (h *Handlers) CronJobCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if h.scheduler == nil {
		writeError(w, http.StatusServiceUnavailable, "cron scheduler is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var payload cronJobMutationPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	st, err := h.scheduler.CreatePersistent(agentcron.Job{
		Name:        strings.TrimSpace(payload.Name),
		Schedule:    strings.TrimSpace(payload.Schedule),
		Trigger:     strings.TrimSpace(payload.Trigger),
		Prompt:      payload.Prompt,
		Channel:     strings.TrimSpace(payload.Channel),
		SessionMode: strings.TrimSpace(payload.SessionMode),
		TimeoutSec:  payload.TimeoutSec,
		Enabled:     enabled,
		Source:      "db",
	})
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "duplicate") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":  true,
		"job": cronJobStatusPayload(st),
	})
}

// CronJobUpdate updates one DB-backed cron job and hot-reloads runtime schedule.
func (h *Handlers) CronJobUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "use PUT")
		return
	}
	if h.scheduler == nil {
		writeError(w, http.StatusServiceUnavailable, "cron scheduler is not configured")
		return
	}
	name := extractCronJobNameWithSuffix(r.URL.Path, "/api/v1/cron/jobs/", "")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing cron job name")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var payload cronJobMutationPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	st, err := h.scheduler.UpdatePersistent(name, agentcron.Job{
		Schedule:    strings.TrimSpace(payload.Schedule),
		Trigger:     strings.TrimSpace(payload.Trigger),
		Prompt:      payload.Prompt,
		Channel:     strings.TrimSpace(payload.Channel),
		SessionMode: strings.TrimSpace(payload.SessionMode),
		TimeoutSec:  payload.TimeoutSec,
	})
	if err != nil {
		msg := strings.ToLower(err.Error())
		switch {
		case strings.Contains(msg, "not found"):
			writeError(w, http.StatusNotFound, err.Error())
		case strings.Contains(msg, "read-only"):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":  true,
		"job": cronJobStatusPayload(st),
	})
}

type cronJobEnabledPayload struct {
	Enabled *bool `json:"enabled"`
}

// CronJobSetEnabled toggles one DB-backed cron job.
func (h *Handlers) CronJobSetEnabled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "use PUT")
		return
	}
	if h.scheduler == nil {
		writeError(w, http.StatusServiceUnavailable, "cron scheduler is not configured")
		return
	}
	name := extractCronJobNameWithSuffix(r.URL.Path, "/api/v1/cron/jobs/", "/enabled")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing cron job name")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var payload cronJobEnabledPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if payload.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	st, err := h.scheduler.SetPersistentEnabled(name, *payload.Enabled)
	if err != nil {
		msg := strings.ToLower(err.Error())
		switch {
		case strings.Contains(msg, "not found"):
			writeError(w, http.StatusNotFound, err.Error())
		case strings.Contains(msg, "read-only"):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":  true,
		"job": cronJobStatusPayload(st),
	})
}

// CronJobDelete deletes one DB-backed cron job.
func (h *Handlers) CronJobDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "use DELETE")
		return
	}
	if h.scheduler == nil {
		writeError(w, http.StatusServiceUnavailable, "cron scheduler is not configured")
		return
	}
	name := extractCronJobNameWithSuffix(r.URL.Path, "/api/v1/cron/jobs/", "")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing cron job name")
		return
	}
	if err := h.scheduler.DeletePersistent(name); err != nil {
		msg := strings.ToLower(err.Error())
		switch {
		case strings.Contains(msg, "not found"):
			writeError(w, http.StatusNotFound, err.Error())
		case strings.Contains(msg, "read-only"):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"deleted": name,
	})
}

type agentProfileCreatePayload struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	IsActive     *bool  `json:"is_active,omitempty"`
}

// Agents lists or creates agent profiles.
func (h *Handlers) Agents(w http.ResponseWriter, r *http.Request) {
	if h.agentProfiles == nil {
		writeError(w, http.StatusServiceUnavailable, "agent profile store is not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		profiles, err := h.agentProfiles.List()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list agent profiles: "+err.Error())
			return
		}
		activeID := ""
		for _, p := range profiles {
			if p.IsActive {
				activeID = p.ID
				break
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"profiles":  profiles,
			"count":     len(profiles),
			"active_id": activeID,
		})
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		var payload agentProfileCreatePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		name := strings.TrimSpace(payload.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		isActive := false
		if payload.IsActive != nil {
			isActive = *payload.IsActive
		}
		profile, err := h.agentProfiles.Create(storage.AgentProfile{
			Name:         name,
			Description:  strings.TrimSpace(payload.Description),
			Provider:     strings.TrimSpace(payload.Provider),
			Model:        strings.TrimSpace(payload.Model),
			SystemPrompt: payload.SystemPrompt,
			IsActive:     isActive,
		})
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"profile": profile,
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

type agentProfileUpdatePayload struct {
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	Provider     *string `json:"provider,omitempty"`
	Model        *string `json:"model,omitempty"`
	SystemPrompt *string `json:"system_prompt,omitempty"`
	IsActive     *bool   `json:"is_active,omitempty"`
}

// AgentProfileUpdate updates one agent profile.
func (h *Handlers) AgentProfileUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "use PUT")
		return
	}
	if h.agentProfiles == nil {
		writeError(w, http.StatusServiceUnavailable, "agent profile store is not configured")
		return
	}
	id := extractPathNameWithSuffix(r.URL.Path, "/api/v1/agents/", "")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing agent profile id")
		return
	}
	current, err := h.agentProfiles.Get(id)
	if err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "agent profile not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to get agent profile: "+err.Error())
		}
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var payload agentProfileUpdatePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	next := *current
	if payload.Name != nil {
		next.Name = strings.TrimSpace(*payload.Name)
	}
	if payload.Description != nil {
		next.Description = strings.TrimSpace(*payload.Description)
	}
	if payload.Provider != nil {
		next.Provider = strings.TrimSpace(*payload.Provider)
	}
	if payload.Model != nil {
		next.Model = strings.TrimSpace(*payload.Model)
	}
	if payload.SystemPrompt != nil {
		next.SystemPrompt = *payload.SystemPrompt
	}
	if strings.TrimSpace(next.Name) == "" {
		writeError(w, http.StatusBadRequest, "name cannot be empty")
		return
	}

	if err := h.agentProfiles.Update(id, next); err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "agent profile not found")
			return
		}
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if payload.IsActive != nil && *payload.IsActive {
		if err := h.agentProfiles.Activate(id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to activate agent profile: "+err.Error())
			return
		}
	}
	updated, err := h.agentProfiles.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load updated profile: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"profile": updated,
	})
}

// AgentProfileDelete deletes one agent profile.
func (h *Handlers) AgentProfileDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "use DELETE")
		return
	}
	if h.agentProfiles == nil {
		writeError(w, http.StatusServiceUnavailable, "agent profile store is not configured")
		return
	}
	id := extractPathNameWithSuffix(r.URL.Path, "/api/v1/agents/", "")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing agent profile id")
		return
	}
	if err := h.agentProfiles.Delete(id); err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "agent profile not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to delete agent profile: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"deleted": id,
	})
}

// AgentProfileActivate sets one profile as active.
func (h *Handlers) AgentProfileActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if h.agentProfiles == nil {
		writeError(w, http.StatusServiceUnavailable, "agent profile store is not configured")
		return
	}
	id := extractPathNameWithSuffix(r.URL.Path, "/api/v1/agents/", "/activate")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing agent profile id")
		return
	}
	if err := h.agentProfiles.Activate(id); err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "agent profile not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to activate agent profile: "+err.Error())
		}
		return
	}
	profile, err := h.agentProfiles.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load active profile: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"profile": profile,
	})
}

type skillInstallPayload struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Enabled *bool  `json:"enabled,omitempty"`
}

// Skills returns user-defined markdown skills and supports skill installation.
func (h *Handlers) Skills(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		entries, err := workspace.ListSkillEntries(h.dataDir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list skills: "+err.Error())
			return
		}
		skills := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.Enabled {
				skills = append(skills, e.Name)
			}
		}
		sort.Strings(skills)
		writeJSON(w, http.StatusOK, map[string]any{
			"skills":        skills,
			"entries":       entries,
			"count":         len(entries),
			"enabled_count": len(skills),
		})
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
		var payload skillInstallPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		enabled := true
		if payload.Enabled != nil {
			enabled = *payload.Enabled
		}
		if err := workspace.InstallSkill(h.dataDir, payload.Name, payload.Content, enabled); err != nil {
			writeError(w, http.StatusBadRequest, "failed to install skill: "+err.Error())
			return
		}
		state := "enabled"
		if !enabled {
			state = "disabled"
		}
		h.addDebugEvent("skill_install", "ok", "skill installed", map[string]any{
			"name":    strings.TrimSpace(payload.Name),
			"enabled": enabled,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    true,
			"name":  strings.TrimSpace(payload.Name),
			"state": state,
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

// SkillSetEnabled toggles one skill between enabled/disabled states.
func (h *Handlers) SkillSetEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "use PUT")
		return
	}
	suffix := "/disable"
	if enabled {
		suffix = "/enable"
	}
	name := extractPathNameWithSuffix(r.URL.Path, "/api/v1/skills/", suffix)
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing skill name")
		return
	}
	if err := workspace.SetSkillEnabled(h.dataDir, name, enabled); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.addDebugEvent("skill_toggle", "ok", "skill state changed", map[string]any{
		"name":    name,
		"enabled": enabled,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"name":    name,
		"enabled": enabled,
	})
}

// SkillDelete removes one skill file.
func (h *Handlers) SkillDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "use DELETE")
		return
	}
	name := extractPathNameWithSuffix(r.URL.Path, "/api/v1/skills/", "")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing skill name")
		return
	}
	if err := workspace.DeleteSkill(h.dataDir, name); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.addDebugEvent("skill_delete", "ok", "skill deleted", map[string]any{"name": name})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"deleted": name,
	})
}

// Nodes returns MCP and runtime node/tool inventory.
func (h *Handlers) Nodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	type mcpServerRow struct {
		Name                string `json:"name"`
		Command             string `json:"command"`
		ArgsCount           int    `json:"args_count"`
		EnvVarHint          int    `json:"env_var_hint"`
		Status              string `json:"status,omitempty"`
		Healthy             bool   `json:"healthy"`
		LastHealthCheck     string `json:"last_health_check,omitempty"`
		LastHealthError     string `json:"last_health_error,omitempty"`
		RestartCount        int    `json:"restart_count,omitempty"`
		ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
		NextRetryAt         string `json:"next_retry_at,omitempty"`
		RetryBackoffMs      int64  `json:"retry_backoff_ms,omitempty"`
		Disabled            bool   `json:"disabled,omitempty"`
	}
	statusByName := make(map[string]MCPRuntimeStatus)
	if h.mcpStatus != nil {
		for _, st := range h.mcpStatus.SnapshotMCPStatus() {
			statusByName[st.Name] = st
		}
	}
	servers := make([]mcpServerRow, 0, len(h.mcpServers))
	for _, s := range h.mcpServers {
		row := mcpServerRow{
			Name:       s.Name,
			Command:    s.Command,
			ArgsCount:  len(s.Args),
			EnvVarHint: len(s.Env),
		}
		if st, ok := statusByName[s.Name]; ok {
			row.Status = st.Status
			row.Healthy = st.Healthy
			row.LastHealthCheck = st.LastHealthCheck
			row.LastHealthError = st.LastHealthError
			row.RestartCount = st.RestartCount
			row.ConsecutiveFailures = st.ConsecutiveFailures
			row.NextRetryAt = st.NextRetryAt
			row.RetryBackoffMs = st.RetryBackoffMs
			row.Disabled = st.Disabled
		}
		servers = append(servers, row)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })

	var toolNames []string
	if h.agent != nil {
		toolNames = h.agent.ToolNames()
	}
	sort.Strings(toolNames)
	mcpTools := make([]string, 0)
	for _, name := range toolNames {
		if strings.HasPrefix(name, "mcp_") {
			mcpTools = append(mcpTools, name)
		}
	}

	grpcPort := 0
	if h.cfg != nil {
		grpcPort = h.cfg.Gateway.GRPCPort
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"mcp_servers": servers,
		"mcp_tools":   mcpTools,
		"grpc": map[string]any{
			"enabled": grpcPort > 0,
			"port":    grpcPort,
		},
		"tools_total": len(toolNames),
	})
}

type nodeActionPayload struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

// NodeAction handles node-level runtime actions (capabilities, test, restart).
func (h *Handlers) NodeAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var payload nodeActionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	name := strings.TrimSpace(payload.Name)
	action := strings.TrimSpace(strings.ToLower(payload.Action))
	if name == "" || action == "" {
		writeError(w, http.StatusBadRequest, "name and action are required")
		return
	}

	var target *config.MCPServerConfig
	for i := range h.mcpServers {
		if h.mcpServers[i].Name == name {
			target = &h.mcpServers[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	switch action {
	case "capabilities":
		out := map[string]any{
			"name":      target.Name,
			"command":   target.Command,
			"args":      target.Args,
			"env_count": len(target.Env),
			"actions":   []string{"capabilities", "test", "restart"},
		}
		h.addDebugEvent("node_capabilities", "ok", "node capabilities requested", map[string]any{"name": name})
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"name":    name,
			"action":  action,
			"details": out,
		})
	case "test":
		commandPath := target.Command
		if strings.TrimSpace(commandPath) == "" {
			writeError(w, http.StatusBadRequest, "node command is empty")
			return
		}
		_, err := exec.LookPath(commandPath)
		healthy := err == nil
		status := "ok"
		msg := "node command resolved"
		if err != nil {
			status = "error"
			msg = err.Error()
		}
		h.addDebugEvent("node_test", status, "node test executed", map[string]any{
			"name":    name,
			"healthy": healthy,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      healthy,
			"name":    name,
			"action":  action,
			"healthy": healthy,
			"error":   msg,
		})
	case "restart":
		if h.mcpStatus == nil {
			h.addDebugEvent("node_restart", "unavailable", "node restart requested but mcp runtime source is unavailable", map[string]any{"name": name})
			writeError(w, http.StatusServiceUnavailable, "mcp runtime status is unavailable")
			return
		}
		if err := h.mcpStatus.RestartMCPServer(name); err != nil {
			h.addDebugEvent("node_restart", "error", "node restart failed", map[string]any{"name": name, "error": err.Error()})
			writeError(w, http.StatusInternalServerError, "node restart failed: "+err.Error())
			return
		}
		h.addDebugEvent("node_restart", "ok", "node restart requested", map[string]any{"name": name})
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"name":   name,
			"action": action,
		})
	default:
		writeError(w, http.StatusBadRequest, "action must be one of: capabilities|test|restart")
	}
}

// Debug returns runtime diagnostics useful for operators.
func (h *Handlers) Debug(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	setupRequired, setupReason := h.setupState()

	provider := ""
	model := ""
	if h.cfg != nil {
		provider = h.cfg.Model.Provider
		model = h.cfg.Model.Model
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"runtime": map[string]any{
			"go_version":        runtime.Version(),
			"goos":              runtime.GOOS,
			"goarch":            runtime.GOARCH,
			"num_cpu":           runtime.NumCPU(),
			"goroutines":        runtime.NumGoroutine(),
			"heap_alloc_bytes":  mem.HeapAlloc,
			"heap_inuse_bytes":  mem.HeapInuse,
			"sys_bytes":         mem.Sys,
			"gc_cycles":         mem.NumGC,
			"last_gc_unix_nano": mem.LastGC,
		},
		"agent": map[string]any{
			"provider":       provider,
			"model":          model,
			"setup_required": setupRequired,
			"setup_reason":   setupReason,
		},
		"paths": map[string]any{
			"data_dir": h.dataDir,
		},
		"events_count": len(h.listDebugEvents(1000000)),
	})
}

type debugActionPayload struct {
	Action string `json:"action"`
}

// DebugAction executes a runtime debug action.
func (h *Handlers) DebugAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var payload debugActionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	action := strings.TrimSpace(strings.ToLower(payload.Action))
	if action == "" {
		writeError(w, http.StatusBadRequest, "action is required")
		return
	}

	switch action {
	case "ping":
		event := h.addDebugEvent("debug_ping", "ok", "debug ping", nil)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"action": action,
			"event":  event,
		})
	case "gc":
		before := runtime.NumGoroutine()
		runtime.GC()
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		event := h.addDebugEvent("debug_gc", "ok", "manual gc completed", map[string]any{
			"goroutines": before,
			"heap_alloc": mem.HeapAlloc,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"action": action,
			"event":  event,
			"runtime": map[string]any{
				"goroutines":       runtime.NumGoroutine(),
				"heap_alloc_bytes": mem.HeapAlloc,
				"gc_cycles":        mem.NumGC,
			},
		})
	case "stack_dump":
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		snippet := string(buf[:n])
		const max = 12000
		truncated := false
		if len(snippet) > max {
			snippet = snippet[:max]
			truncated = true
		}
		event := h.addDebugEvent("debug_stack_dump", "ok", "stack dump captured", map[string]any{
			"bytes":     n,
			"truncated": truncated,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"action":     action,
			"event":      event,
			"bytes":      n,
			"truncated":  truncated,
			"stack_dump": snippet,
		})
	default:
		writeError(w, http.StatusBadRequest, "action must be one of: ping|gc|stack_dump")
	}
}

// DebugEvents returns recent debug/action events.
func (h *Handlers) DebugEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	limit := parseIntQuery(r, "limit", 100, 1, 500)
	events := h.listDebugEvents(limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"events": events,
		"count":  len(events),
	})
}

// Logs returns recent lines from the configured log file.
func (h *Handlers) Logs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if h.cfg == nil {
		writeError(w, http.StatusServiceUnavailable, "config not available")
		return
	}
	lines := parseIntQuery(r, "lines", 200, 10, 2000)
	contains := strings.TrimSpace(r.URL.Query().Get("contains"))
	level := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("level")))
	logPath := strings.TrimSpace(h.cfg.Logging.Output)
	if logPath == "" || logPath == "stderr" || logPath == "stdout" {
		writeError(w, http.StatusBadRequest, "logging output is not a file path")
		return
	}
	logPath = expandHomePath(logPath)

	chunks, err := tailFileLines(logPath, lines)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading logs failed: "+err.Error())
		return
	}
	for i := range chunks {
		chunks[i] = logger.ScrubSecrets(chunks[i])
	}
	chunks = filterLogLines(chunks, contains, level)
	writeJSON(w, http.StatusOK, map[string]any{
		"path":            logPath,
		"lines_requested": lines,
		"lines_returned":  len(chunks),
		"contains":        contains,
		"level":           level,
		"lines":           chunks,
	})
}

// LogsExport exports filtered log lines as text or JSON.
func (h *Handlers) LogsExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if h.cfg == nil {
		writeError(w, http.StatusServiceUnavailable, "config not available")
		return
	}
	lines := parseIntQuery(r, "lines", 500, 10, 10000)
	contains := strings.TrimSpace(r.URL.Query().Get("contains"))
	level := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("level")))
	format := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("format")))
	if format == "" {
		format = "text"
	}

	logPath := strings.TrimSpace(h.cfg.Logging.Output)
	if logPath == "" || logPath == "stderr" || logPath == "stdout" {
		writeError(w, http.StatusBadRequest, "logging output is not a file path")
		return
	}
	logPath = expandHomePath(logPath)
	chunks, err := tailFileLines(logPath, lines)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading logs failed: "+err.Error())
		return
	}
	for i := range chunks {
		chunks[i] = logger.ScrubSecrets(chunks[i])
	}
	chunks = filterLogLines(chunks, contains, level)

	fileName := "logs-export-" + time.Now().UTC().Format("20060102-150405")
	switch format {
	case "text", "txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+fileName+".txt\"")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Join(chunks, "\n") + "\n"))
	case "json":
		writeJSON(w, http.StatusOK, map[string]any{
			"path":            logPath,
			"format":          "json",
			"contains":        contains,
			"level":           level,
			"lines_requested": lines,
			"lines_returned":  len(chunks),
			"lines":           chunks,
			"file_name":       fileName + ".json",
		})
	default:
		writeError(w, http.StatusBadRequest, "format must be text or json")
	}
}

// OpenAPIDoc returns docs/openapi.yaml for the embedded docs page.
func (h *Handlers) OpenAPIDoc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	path, err := findOpenAPIPath()
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading openapi file failed: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func parseIntQuery(r *http.Request, key string, def, min, max int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func extractPathNameWithSuffix(path, prefix, suffix string) string {
	raw := strings.TrimPrefix(path, prefix)
	raw = strings.TrimSpace(raw)
	if suffix != "" {
		raw = strings.TrimSuffix(raw, suffix)
	}
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return ""
	}
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	return strings.TrimSpace(raw)
}

func cronJobStatusPayload(st agentcron.JobStatus) map[string]any {
	out := map[string]any{
		"id":              st.ID,
		"name":            st.Name,
		"schedule":        st.Schedule,
		"trigger":         st.Trigger,
		"channel":         st.Channel,
		"session_mode":    st.SessionMode,
		"timeout_seconds": st.TimeoutSec,
		"enabled":         st.Enabled,
		"source":          st.Source,
	}
	if !st.LastRun.IsZero() {
		out["last_run"] = st.LastRun.UTC().Format(time.RFC3339)
	}
	if !st.NextRun.IsZero() {
		out["next_run"] = st.NextRun.UTC().Format(time.RFC3339)
	}
	return out
}

func extractCronJobNameWithSuffix(path, prefix, suffix string) string {
	return extractPathNameWithSuffix(path, prefix, suffix)
}

func expandHomePath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func filterLogLines(lines []string, contains, level string) []string {
	contains = strings.TrimSpace(strings.ToLower(contains))
	level = strings.TrimSpace(strings.ToLower(level))
	if contains == "" && level == "" {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		lower := strings.ToLower(line)
		if contains != "" && !strings.Contains(lower, contains) {
			continue
		}
		if level != "" {
			// Support common log formats: JSON (`"level":"info"`) and key/value (`level=info`).
			if !strings.Contains(lower, "\"level\":\""+level+"\"") && !strings.Contains(lower, "level="+level) {
				continue
			}
		}
		out = append(out, line)
	}
	return out
}

func tailFileLines(path string, limit int) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if limit <= 0 || len(lines) <= limit {
		return lines, nil
	}
	return lines[len(lines)-limit:], nil
}

func findOpenAPIPath() (string, error) {
	candidates := []string{
		filepath.Join("docs", "openapi.yaml"),
	}
	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(base, "docs", "openapi.yaml"),
			filepath.Join(base, "..", "docs", "openapi.yaml"),
			filepath.Join(base, "..", "..", "docs", "openapi.yaml"),
		)
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("openapi spec not found (expected docs/openapi.yaml)")
}
