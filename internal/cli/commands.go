package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openclio/openclio/internal/cost"
	"github.com/openclio/openclio/internal/storage"
)

// HandleCommand processes a slash command. Returns true if it was handled.
func (c *CLI) HandleCommand(input string) bool {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return false
	}

	cmd := strings.ToLower(parts[0])
	switch cmd {
	case "/help":
		c.cmdHelp()
		return true
	case "/sessions":
		c.cmdSessions()
		return true
	case "/new":
		c.cmdNew()
		return true
	case "/clear":
		c.cmdReset()
		return true
	case "/reset":
		c.cmdReset()
		return true
	case "/compact":
		c.cmdCompact()
		return true
	case "/history":
		c.cmdHistory()
		return true
	case "/usage":
		c.cmdUsage()
		return true
	case "/cost":
		c.cmdCost()
		return true
	case "/model":
		c.cmdModel()
		return true
	case "/memory":
		c.cmdMemory()
		return true
	case "/skill", "/skills":
		c.cmdSkill(parts)
		return true
	case "/debug":
		if len(parts) > 1 {
			if parts[1] == "context" {
				c.cmdDebugContext()
				return true
			}
			if parts[1] == "tokens" {
				c.cmdDebugTokens()
				return true
			}
		}
		PrintError("Usage: /debug context | /debug tokens")
		return true
	default:
		if strings.HasPrefix(cmd, "/") {
			PrintError(fmt.Sprintf("Unknown command: %s (type /help)", cmd))
			return true
		}
		return false
	}
}

func (c *CLI) cmdHelp() {
	fmt.Println()
	fmt.Printf("%sAvailable commands:%s\n\n", colorBold(), colorReset())
	fmt.Printf("  %s/help%s       Show this help\n", colorCyan(), colorReset())
	fmt.Printf("  %s/sessions%s   List recent sessions\n", colorCyan(), colorReset())
	fmt.Printf("  %s/new%s        Start a new session (keep old history)\n", colorCyan(), colorReset())
	fmt.Printf("  %s/clear%s      Clear current session and start fresh\n", colorCyan(), colorReset())
	fmt.Printf("  %s/compact%s    Compact old history into a summary\n", colorCyan(), colorReset())
	fmt.Printf("  %s/reset%s      Clear current session and start fresh\n", colorCyan(), colorReset())
	fmt.Printf("  %s/history%s    Show messages in current session\n", colorCyan(), colorReset())
	fmt.Printf("  %s/model%s      Show the active model and provider\n", colorCyan(), colorReset())
	fmt.Printf("  %s/skill%s      Show workspace and configured jobs\n", colorCyan(), colorReset())
	fmt.Printf("  %s/usage%s      Show cumulative token usage\n", colorCyan(), colorReset())
	fmt.Printf("  %s/cost%s       Show token/cost summary\n", colorCyan(), colorReset())
	fmt.Printf("  %s/memory%s     Show persistent memory notes\n", colorCyan(), colorReset())
	fmt.Printf("  %sexit%s        Quit the agent\n", colorCyan(), colorReset())
	fmt.Println()
}

func (c *CLI) cmdSessions() {
	sessions, err := c.sessions.List(10)
	if err != nil {
		PrintError("Failed to list sessions: " + err.Error())
		return
	}

	infos := make([]SessionInfo, len(sessions))
	for i, s := range sessions {
		infos[i] = SessionInfo{
			ID:         s.ID,
			Channel:    s.Channel,
			LastActive: s.LastActive.Format("2006-01-02 15:04"),
		}
	}
	PrintSessionList(infos)
}

func (c *CLI) cmdNew() {
	session, err := c.sessions.Create("cli", "local")
	if err != nil {
		PrintError("Failed to create session: " + err.Error())
		return
	}
	c.sessionID = session.ID
	c.totalUsage = totalUsage{}
	PrintInfo(fmt.Sprintf("New session: %s", session.ID[:8]+"..."))
}

func (c *CLI) cmdHistory() {
	if c.sessionID == "" {
		PrintError("No active session.")
		return
	}

	messages, err := c.messages.GetBySession(c.sessionID)
	if err != nil {
		PrintError("Failed to get history: " + err.Error())
		return
	}

	if len(messages) == 0 {
		PrintInfo("No messages yet.")
		return
	}

	fmt.Println()
	for _, m := range messages {
		roleColor := colorDim()
		switch m.Role {
		case "user":
			roleColor = colorBold()
		case "assistant":
			roleColor = colorGreen()
		}

		content := m.Content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		fmt.Printf("  %s[%s]%s %s\n", roleColor, m.Role, colorReset(), content)
	}
	fmt.Println()
}

func (c *CLI) cmdUsage() {
	fmt.Println()
	fmt.Printf("  %sTotal Tokens:%s %d in / %d out\n", colorDim(), colorReset(), c.totalUsage.inputTokens, c.totalUsage.outputTokens)
	fmt.Printf("  %sLLM Calls:%s    %d\n", colorDim(), colorReset(), c.totalUsage.llmCalls)

	if c.costTracker != nil {
		if summary, err := c.costTracker.GetSummaryBySession(c.sessionID); err == nil && summary.CallCount > 0 {
			fmt.Printf("  %sCost Check:%s   ~$%.4f\n", colorDim(), colorReset(), summary.TotalCost)
		} else {
			fmt.Printf("  %sCost Check:%s   ~$%.4f\n", colorDim(), colorReset(), 0.0)
		}
	} else {
		fmt.Printf("  %sCost Check:%s   not available (no tracker)\n", colorDim(), colorReset())
	}

	fmt.Println()
}

func (c *CLI) cmdCost() {
	if c.costTracker == nil {
		PrintError("Cost tracking is not enabled.")
		return
	}

	summaries := make(map[string]*cost.Summary)
	for _, period := range []string{"today", "week", "month", "all"} {
		if s, err := c.costTracker.GetSummary(period); err == nil {
			summaries[period] = s
		}
	}
	byProvider, _ := c.costTracker.ProviderBreakdown("all")
	currentSession, _ := c.costTracker.GetSummaryBySession(c.sessionID)

	fmt.Println()
	fmt.Print(cost.FormatSummary(summaries, byProvider, currentSession))
}

func (c *CLI) cmdMemory() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		PrintError("Failed to resolve home directory: " + err.Error())
		return
	}

	memoryPath := filepath.Join(homeDir, ".openclio", "memory.md")
	data, err := os.ReadFile(memoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			PrintInfo("No memory saved yet. The agent will populate memory over time.")
			return
		}
		PrintError("Failed to read memory: " + err.Error())
		return
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		PrintInfo("Memory file is currently empty.")
		return
	}

	fmt.Println()
	fmt.Printf("%sPersistent memory (%s):%s\n", colorBold(), memoryPath, colorReset())
	fmt.Println(content)
	fmt.Println()
}

// cmdReset clears the current session and starts a fresh one.
func (c *CLI) cmdReset() {
	if c.sessionID != "" {
		if err := c.sessions.Delete(c.sessionID); err != nil {
			PrintError("Failed to delete session: " + err.Error())
		}
	}
	session, err := c.sessions.Create("cli", "local")
	if err != nil {
		PrintError("Failed to create session: " + err.Error())
		return
	}
	c.sessionID = session.ID
	c.totalUsage = totalUsage{}
	PrintInfo("🗑  Session cleared. New session: " + session.ID[:8] + "...")
}

func (c *CLI) cmdCompact() {
	if c.sessionID == "" {
		PrintError("No active session.")
		return
	}

	msgProvider := &cliMessageProvider{
		messages:  c.messages,
		sessionID: c.sessionID,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := c.agent.ForceCompaction(ctx, c.sessionID, msgProvider, nil); err != nil {
		PrintError("Compaction failed: " + err.Error())
		return
	}
	PrintInfo("Compaction complete.")
}

// cmdModel prints the active LLM provider and model.
func (c *CLI) cmdModel() {
	fmt.Println()
	fmt.Printf("  %sProvider:%s  %s\n", colorDim(), colorReset(), c.provider)
	fmt.Printf("  %sModel:%s     %s\n", colorDim(), colorReset(), c.model)
	fmt.Printf("  %sHint:%s      Change via model.provider/model.model in config.yaml\n", colorDim(), colorReset())
	fmt.Println()
}

// cmdSkill loads a skill into the current session, or prints workspace/cron status.
func (c *CLI) cmdSkill(parts []string) {
	if len(parts) == 1 {
		// Old behaviour: just print workspace and cron jobs
		fmt.Println()
		if c.workspaceName != "" {
			fmt.Printf("  %sWorkspace:%s  %s\n", colorDim(), colorReset(), c.workspaceName)
		} else {
			fmt.Printf("  %sWorkspace:%s  (not configured — add ~/.openclio/workspace.yaml)\n", colorDim(), colorReset())
		}
		if len(c.cronJobs) > 0 {
			fmt.Printf("  %sScheduled jobs:%s\n", colorDim(), colorReset())
			for _, j := range c.cronJobs {
				fmt.Printf("    • %s%s%s\n", colorCyan(), j, colorReset())
			}
		} else {
			fmt.Printf("  %sScheduled jobs:%s  none (add cron: entries in config.yaml)\n", colorDim(), colorReset())
		}
		fmt.Println()
		return
	}

	skillName := parts[1]
	homeDir, err := os.UserHomeDir()
	if err != nil {
		PrintError("Failed to get home dir: " + err.Error())
		return
	}

	skillPath := filepath.Join(homeDir, ".openclio", "skills", skillName+".md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		if os.IsNotExist(err) {
			PrintError(fmt.Sprintf("Skill '%s' not found in ~/.openclio/skills/", skillName))
			return
		}
		PrintError("Failed to read skill: " + err.Error())
		return
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		PrintError("Skill file is empty.")
		return
	}

	// Insert as a system message so the LLM internalizes the instruction
	// Using 0 for tokens as it's a dynamic system injection
	_, err = c.messages.Insert(c.sessionID, "system", fmt.Sprintf("[Skill Instruction: %s]\n%s", skillName, content), 0)
	if err != nil {
		PrintError("Failed to inject skill into session: " + err.Error())
		return
	}

	PrintInfo(fmt.Sprintf("✅ Loaded skill '%s' into the current session.", skillName))
}

func (c *CLI) cmdDebugContext() {
	if c.lastContext == nil {
		PrintError("No context assembled yet in this session.")
		return
	}
	fmt.Println()
	fmt.Printf("%s--- Last Assembled Context ---%s\n", colorBold(), colorReset())
	fmt.Printf("%sSystem Prompt:%s\n%s\n\n", colorCyan(), colorReset(), c.lastContext.SystemPrompt)
	fmt.Printf("%sMessages (%d):%s\n", colorCyan(), len(c.lastContext.Messages), colorReset())
	for i, m := range c.lastContext.Messages {
		content := m.Content
		if len(content) > 300 {
			content = content[:300] + "... (truncated)"
		}
		fmt.Printf("  [%d] %s%s%s: %s\n", i+1, colorBold(), m.Role, colorReset(), content)
	}
	fmt.Println()
}

func (c *CLI) cmdDebugTokens() {
	if c.lastContext == nil {
		PrintError("No context assembled yet in this session.")
		return
	}
	st := c.lastContext.Stats
	fmt.Println()
	fmt.Printf("%s--- Last Context Token Breakdown ---%s\n", colorBold(), colorReset())
	fmt.Printf("  System Prompt:      %d\n", st.SystemPromptTokens)
	fmt.Printf("  User Message:       %d\n", st.UserMessageTokens)
	fmt.Printf("  Recent Turns:       %d\n", st.RecentTurnTokens)
	fmt.Printf("  Retrieved History:  %d (from %d messages)\n", st.RetrievedHistoryTokens, st.RetrievedMessagesCount)
	fmt.Printf("  Semantic Memory:    %d\n", st.SemanticMemoryTokens)
	fmt.Printf("  Tool Definitions:   %d\n", st.ToolDefTokens)
	fmt.Printf("  --------------------------\n")
	fmt.Printf("  Total Context:      %d / %d budget limit\n", st.TotalTokens, st.BudgetTotal)
	fmt.Printf("  Remaining Budget:   %d\n", st.BudgetRemaining)
	fmt.Println()
}

// Ensure storage types are used.
var _ = (*storage.Session)(nil)
