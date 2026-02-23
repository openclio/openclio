package cli

import (
	"fmt"
	"os"
	"sync"
)

// colorEnabled controls whether ANSI codes are output.
// It is set once at init based on whether stdout is a terminal.
var (
	colorOnce    sync.Once
	colorEnabled bool
)

func initColor() {
	colorOnce.Do(func() {
		// os.Stdout.Stat is available on all platforms and avoids external deps.
		stat, err := os.Stdout.Stat()
		colorEnabled = err == nil && (stat.Mode()&os.ModeCharDevice != 0)
	})
}

// SetColorEnabled overrides TTY detection. Useful in tests.
func SetColorEnabled(v bool) {
	colorOnce.Do(func() {}) // ensure once is triggered so override takes effect
	colorEnabled = v
}

func ansi(code string) string {
	if colorEnabled {
		return code
	}
	return ""
}

// ANSI color codes — only emitted when stdout is a TTY.
func colorReset() string  { return ansi("\033[0m") }
func colorGreen() string  { return ansi("\033[32m") }
func colorYellow() string { return ansi("\033[33m") }
func colorCyan() string   { return ansi("\033[36m") }
func colorDim() string    { return ansi("\033[2m") }
func colorBold() string   { return ansi("\033[1m") }
func colorRed() string    { return ansi("\033[31m") }

func init() { initColor() }

// PrintWelcome displays the startup banner.
func PrintWelcome(sessionID, provider, model string) {
	fmt.Printf("\n%s%s╭──────────────────────────────────────╮%s\n", colorBold(), colorCyan(), colorReset())
	fmt.Printf("%s%s│        🤖 Agent — Chat Mode          │%s\n", colorBold(), colorCyan(), colorReset())
	fmt.Printf("%s%s╰──────────────────────────────────────╯%s\n\n", colorBold(), colorCyan(), colorReset())
	fmt.Printf("  %sProvider:%s  %s (%s)\n", colorDim(), colorReset(), provider, model)
	fmt.Printf("  %sSession:%s   %s\n", colorDim(), colorReset(), sessionID[:8]+"...")
	fmt.Printf("  %sCommands:%s  /help for slash commands\n", colorDim(), colorReset())
	fmt.Printf("  %sExit:%s      Ctrl+C, exit, or quit\n\n", colorDim(), colorReset())
}

// PrintAssistant prints the assistant response.
func PrintAssistant(text string) {
	fmt.Printf("\n%s%s⚡ Agent%s\n", colorBold(), colorGreen(), colorReset())
	fmt.Printf("%s%s%s\n", colorGreen(), text, colorReset())
}

// PrintToolCall displays a tool invocation.
func PrintToolCall(name string, args string, result string, errMsg string) {
	fmt.Printf("\n  %s🔧 %s%s", colorYellow(), name, colorReset())
	if len(args) > 0 && args != "{}" {
		display := args
		if len(display) > 80 {
			display = display[:80] + "..."
		}
		fmt.Printf(" %s%s%s", colorDim(), display, colorReset())
	}
	fmt.Println()

	if errMsg != "" {
		fmt.Printf("  %s✗ %s%s\n", colorRed(), errMsg, colorReset())
		return
	}

	if len(result) > 200 {
		result = result[:200] + "...[truncated]"
	}
	fmt.Printf("  %s→ %s%s\n", colorDim(), result, colorReset())
}

// PrintUsage displays token usage stats.
func PrintUsage(inputTokens, outputTokens, llmCalls int, durationMs int64) {
	fmt.Printf("\n%s  ── tokens: %d in / %d out │ calls: %d │ %dms%s\n",
		colorDim(), inputTokens, outputTokens, llmCalls, durationMs, colorReset())
}

// PrintError displays an error message.
func PrintError(msg string) {
	fmt.Printf("%s✗ %s%s\n", colorRed(), msg, colorReset())
}

// PrintInfo displays an informational message.
func PrintInfo(msg string) {
	fmt.Printf("%s%s%s\n", colorCyan(), msg, colorReset())
}

// PrintSessionList displays sessions.
func PrintSessionList(sessions []SessionInfo) {
	if len(sessions) == 0 {
		PrintInfo("No sessions found.")
		return
	}
	fmt.Printf("\n%s%-10s  %-8s  %-20s%s\n", colorBold(), "ID", "Channel", "Last Active", colorReset())
	fmt.Println("──────────  ────────  ────────────────────")
	for _, s := range sessions {
		fmt.Printf("%-10s  %-8s  %-20s\n", s.ID[:8]+"...", s.Channel, s.LastActive)
	}
	fmt.Println()
}

// SessionInfo is a simplified session for display.
type SessionInfo struct {
	ID         string
	Channel    string
	LastActive string
}
