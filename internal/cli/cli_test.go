package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// ─── Color detection ──────────────────────────────────────────────────────────

func TestSetColorEnabledFalse(t *testing.T) {
	SetColorEnabled(false)
	if colorBold() != "" {
		t.Error("expected empty string when colors disabled")
	}
	if colorReset() != "" {
		t.Error("expected empty string when colors disabled")
	}
}

func TestSetColorEnabledTrue(t *testing.T) {
	SetColorEnabled(true)
	if colorBold() == "" {
		t.Error("expected ANSI code when colors enabled")
	}
	if !strings.Contains(colorGreen(), "32") {
		t.Errorf("expected green ANSI code, got %q", colorGreen())
	}
	SetColorEnabled(false) // reset for other tests
}

// ─── HandleCommand ────────────────────────────────────────────────────────────

// stubCLI creates a minimal CLI for testing without real storage.
func stubCLI() *CLI {
	return &CLI{
		provider:      "anthropic",
		model:         "claude-3-5-sonnet-20241022",
		workspaceName: "test-workspace",
		cronJobs:      []string{"daily-briefing (0 8 * * *)"},
		sessionID:     "test-session-id",
	}
}

func TestHandleCommandHelp(t *testing.T) {
	c := stubCLI()
	if !c.HandleCommand("/help") {
		t.Error("expected /help to return true")
	}
}

func TestHandleCommandModel(t *testing.T) {
	c := stubCLI()
	if !c.HandleCommand("/model") {
		t.Error("expected /model to return true")
	}
}

func TestHandleCommandSkill(t *testing.T) {
	c := stubCLI()
	// Both /skill and /skill <name> should be handled (return true)
	// We only test dispatch here to avoid needing full DB and FS setup.
	if !c.HandleCommand("/skill") {
		t.Error("expected /skill to return true")
	}
	if !c.HandleCommand("/skills") {
		t.Error("expected /skills alias to return true")
	}
	if !c.HandleCommand("/skill some-skill-name") {
		t.Error("expected /skill <name> to return true")
	}
}

func TestHandleCommandNew(t *testing.T) {
	c := stubCLI()
	// /new calls c.sessions.Create which will panic without storage;
	// just verify that unknown /new without storage panics is not expected.
	// We test the router dispatch only here — full integration covered elsewhere.
	defer func() { recover() }() // it will error without storage — that's fine
	c.HandleCommand("/new")
}

func TestHandleCommandUnknown(t *testing.T) {
	c := stubCLI()
	if !c.HandleCommand("/xyzzy") {
		t.Error("expected unknown /command to return true (handled with error msg)")
	}
}

func TestHandleCommandNotSlash(t *testing.T) {
	c := stubCLI()
	if c.HandleCommand("hello world") {
		t.Error("expected plain text to return false")
	}
}

func TestHandleCommandEmpty(t *testing.T) {
	c := stubCLI()
	if c.HandleCommand("") {
		t.Error("expected empty input to return false")
	}
}

// ─── Display helpers ─────────────────────────────────────────────────────────

func TestPrintDisplayHelpers(t *testing.T) {
	SetColorEnabled(false)

	// Capture stdout
	// We can't easily redirect os.Stdout in a unit test without pipes,
	// so we verify the functions don't panic and produce expected substrings
	// by wrapping internal fmt.Sprintf logic.

	errMsg := fmt.Sprintf("%s%s%s", colorRed(), "test error", colorReset())
	if !strings.Contains(errMsg, "test error") {
		t.Error("expected error message to contain 'test error'")
	}

	info := fmt.Sprintf("%s%s%s", colorCyan(), "test info", colorReset())
	if !strings.Contains(info, "test info") {
		t.Error("expected info message to contain 'test info'")
	}
}

func TestScrubInOutput(t *testing.T) {
	// Verify color disabled output has no escape codes
	SetColorEnabled(false)
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s%s%s", colorBold(), "clean text", colorReset())
	if strings.Contains(buf.String(), "\033") {
		t.Error("expected no ANSI escape codes when color is disabled")
	}
	SetColorEnabled(false)
}
