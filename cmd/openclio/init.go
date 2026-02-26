package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/openclio/openclio/internal/workspace"
)

// runInit is the interactive first-time setup wizard.
// It runs BEFORE any config or database is loaded so it works on a fresh install.
func runInit(dataDir string) {
	configPath := filepath.Join(dataDir, "config.yaml")

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                                                                  ║")
	fmt.Println("║   🚀 Welcome to openclio — Your Personal AI Agent               ║")
	fmt.Println("║                                                                  ║")
	fmt.Println("║   Local-first • Private • Yours                                 ║")
	fmt.Println("║                                                                  ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Check for existing config
	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("⚠️  Config already exists at %s\n", configPath)
		if !promptConfirm("Overwrite and start fresh?", false) {
			fmt.Println("Setup cancelled. Your existing config is unchanged.")
			os.Exit(0)
		}
		fmt.Println()
	}

	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", dataDir, err)
		os.Exit(1)
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// STEP 1: Create Your Assistant
	// ═══════════════════════════════════════════════════════════════════════════
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Println("│  STEP 1: Create Your Assistant                                  │")
	fmt.Println("└─────────────────────────────────────────────────────────────────┘")
	fmt.Println()

	assistantName := promptString("What should your assistant be called?", "Aria")
	if assistantName == "" {
		assistantName = "Aria"
	}
	assistantIcon := promptString("Pick an emoji or icon for your assistant (e.g. 🤖 ✨ 🎯)", "🤖")
	if assistantIcon == "" {
		assistantIcon = "🤖"
	}

	fmt.Println()
	fmt.Println("Choose your assistant's personality style:")
	fmt.Println()
	fmt.Println("  1) 🎯 Professional — Direct, efficient, business-focused")
	fmt.Println("  2) 🛠️  Technical — Precise, thorough, code-first mindset")
	fmt.Println("  3) 🎨 Creative — Exploratory, suggestive, brainstorming-oriented")
	fmt.Println("  4) 🧘 Minimal — Ultra-concise, no fluff, just facts")
	fmt.Println("  5) 🌟 Balanced — Friendly mix of all above (default)")
	fmt.Println()
	personalityChoice := promptChoice("Personality style", []string{"1", "2", "3", "4", "5"}, "5")

	personalityTraits := map[string][]string{
		"1": {"Professional", "direct", "efficient", "business-focused", "clear communicator"},
		"2": {"Technical", "precise", "thorough", "code-first", "detail-oriented"},
		"3": {"Creative", "exploratory", "suggestive", "brainstorming", "idea-generator"},
		"4": {"Minimal", "ultra-concise", "no-fluff", "just-the-facts", "high-density"},
		"5": {"Balanced", "friendly", "adaptable", "clear", "helpful"},
	}
	traits := personalityTraits[personalityChoice]

	fmt.Println()

	// ═══════════════════════════════════════════════════════════════════════════
	// STEP 2: About You
	// ═══════════════════════════════════════════════════════════════════════════
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Println("│  STEP 2: Tell Me About You                                      │")
	fmt.Println("└─────────────────────────────────────────────────────────────────┘")
	fmt.Println()
	fmt.Println("This helps me understand your context and communicate better.")
	fmt.Println("(Press Enter to skip any question)")
	fmt.Println()

	userName := promptString("Your name?", "")
	userRole := promptString("Your profession/role?", "")
	userStack := promptString("Primary tech stack? (e.g., 'Go, React, PostgreSQL')", "")

	fmt.Println()
	fmt.Println("How do you prefer responses?")
	fmt.Println()
	fmt.Println("  1) 📝 Detailed — Thorough explanations, examples, context")
	fmt.Println("  2) ⚡ Balanced — Good detail without overwhelming (default)")
	fmt.Println("  3) 🎯 Concise — Bullet points, minimal text, just what you need")
	fmt.Println()
	responseStyle := promptChoice("Response style", []string{"1", "2", "3"}, "2")

	responseStyleLabels := map[string]string{
		"1": "detailed",
		"2": "balanced",
		"3": "concise",
	}

	fmt.Println()

	// Build rich user profile
	var userProfileParts []string
	if userName != "" {
		userProfileParts = append(userProfileParts, fmt.Sprintf("My name is %s.", userName))
	}
	if userRole != "" {
		userProfileParts = append(userProfileParts, fmt.Sprintf("I work as a %s.", userRole))
	}
	if userStack != "" {
		userProfileParts = append(userProfileParts, fmt.Sprintf("My primary tech stack includes: %s.", userStack))
	}
	userProfileParts = append(userProfileParts, fmt.Sprintf("I prefer %s responses.", responseStyleLabels[responseStyle]))

	if len(userProfileParts) == 0 {
		userProfileParts = append(userProfileParts, "I am a developer and prefer concise, practical answers.")
	}
	userProfile := strings.Join(userProfileParts, " ")

	// Install complete template set with the assistant name
	if err := workspace.InstallDefaults(dataDir, assistantName); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to install default templates: %v\n", err)
	}
	// Persist display name and icon for UI and identity
	if err := workspace.SaveAssistantDisplay(dataDir, assistantName, assistantIcon); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save assistant display: %v\n", err)
	}

	// Install user profile using the template
	if err := workspace.InstallUserProfile(dataDir, userProfile); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to install user profile: %v\n", err)
	}

	// Seed bundled default skills for backward compatibility
	if err := workspace.SeedDefaultSkills(dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to seed default skills: %v\n", err)
	}

	fmt.Println()

	// ═══════════════════════════════════════════════════════════════════════════
	// STEP 3: Configure Channels
	// ═══════════════════════════════════════════════════════════════════════════
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Println("│  STEP 3: Configure Channels                                     │")
	fmt.Println("└─────────────────────────────────────────────────────────────────┘")
	fmt.Println()
	fmt.Println("Channels allow you to interact with your agent from different interfaces.")
	fmt.Println()
	fmt.Println("  [✓] CLI (always enabled)")
	fmt.Println()

	enableTelegram := promptConfirm("Enable Telegram?", false)
	enableDiscord := promptConfirm("Enable Discord?", false)
	enableWhatsApp := promptConfirm("Enable WhatsApp?", false)
	enableWebChat := promptConfirm("Enable WebChat (browser UI)?", true)

	fmt.Println()

	// ═══════════════════════════════════════════════════════════════════════════
	// STEP 4: Configure AI Provider
	// ═══════════════════════════════════════════════════════════════════════════
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Println("│  STEP 4: Configure AI Provider                                  │")
	fmt.Println("└─────────────────────────────────────────────────────────────────┘")
	fmt.Println()
	fmt.Println("Choose your AI model provider:")
	fmt.Println()
	fmt.Println("  1) 🖥️  Ollama    — Local, free, fully private (requires local setup)")
	fmt.Println("  2) 🤖 OpenAI     — GPT models, fast, reliable (requires API key)")
	fmt.Println("  3) 🧠 Anthropic  — Claude models, excellent reasoning (requires API key)")
	fmt.Println("  4) ✨ Google     — Gemini models, good balance (requires API key)")
	fmt.Println()
	providerChoice := promptChoice("Choose provider (required)", []string{"1", "2", "3", "4"}, "2")

	var provider, defaultModel, apiKeyEnv, apiKeyHint string
	switch providerChoice {
	case "1":
		provider = "ollama"
		defaultModel = "llama3.1"
		apiKeyEnv = ""
	case "2":
		provider = "openai"
		defaultModel = "gpt-4o-mini"
		apiKeyEnv = "OPENAI_API_KEY"
		apiKeyHint = "https://platform.openai.com/api-keys"
	case "3":
		provider = "anthropic"
		defaultModel = "claude-sonnet-4-20250514"
		apiKeyEnv = "ANTHROPIC_API_KEY"
		apiKeyHint = "https://console.anthropic.com/settings/keys"
	default:
		provider = "gemini"
		defaultModel = "gemini-1.5-flash"
		apiKeyEnv = "GEMINI_API_KEY"
		apiKeyHint = "https://aistudio.google.com/app/apikey"
	}

	fmt.Println()
	model := promptString(fmt.Sprintf("Model name? [%s]", defaultModel), defaultModel)
	fmt.Println()
	portStr := promptString("HTTP port for Web UI? [18789]", "18789")
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		fmt.Fprintf(os.Stderr, "Invalid port: %s\n", portStr)
		os.Exit(1)
	}

	if provider != "ollama" {
		fmt.Println()
		fmt.Printf("API key environment variable? [%s] ", apiKeyEnv)
		customEnv := promptString("", apiKeyEnv)
		if customEnv != "" && customEnv != apiKeyEnv {
			apiKeyEnv = customEnv
		}
	}

	fmt.Println()

	// ═══════════════════════════════════════════════════════════════════════════
	// STEP 5: Generate Configuration
	// ═══════════════════════════════════════════════════════════════════════════
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Println("│  STEP 5: Generating Configuration...                            │")
	fmt.Println("└─────────────────────────────────────────────────────────────────┘")
	fmt.Println()

	// Build YAML
	var sb strings.Builder

	sb.WriteString("# openclio Configuration\n")
	sb.WriteString("# Generated by 'openclio init'\n")
	sb.WriteString("# Edit freely or run 'openclio init' again to reconfigure.\n\n")

	sb.WriteString("# ── AI Model Configuration ─────────────────────────────────────────\n")
	sb.WriteString("model:\n")
	sb.WriteString(fmt.Sprintf("  provider:    %s          # AI provider\n", provider))
	sb.WriteString(fmt.Sprintf("  model:       %s  # Model to use\n", model))
	if apiKeyEnv != "" {
		sb.WriteString(fmt.Sprintf("  api_key_env: %s     # Environment variable for API key\n", apiKeyEnv))
	}
	sb.WriteString("\n")

	sb.WriteString("# ── Gateway (HTTP + WebSocket) ─────────────────────────────────────\n")
	sb.WriteString("gateway:\n")
	sb.WriteString(fmt.Sprintf("  port: %d                    # Port for web UI\n", port))
	sb.WriteString("  bind: 127.0.0.1             # Interface to bind (127.0.0.1 = localhost only)\n")
	sb.WriteString("\n")

	sb.WriteString("# ── Context Engine ─────────────────────────────────────────────────\n")
	sb.WriteString("context:\n")
	sb.WriteString("  max_tokens_per_call:  8000  # Max tokens per LLM call\n")
	sb.WriteString("  history_retrieval_k:  10    # Number of relevant past messages to include\n")
	sb.WriteString("  proactive_compaction: 0.5   # Compact history when context is 50% full\n")
	sb.WriteString("\n")

	sb.WriteString("# ── Tool Configuration ─────────────────────────────────────────────\n")
	sb.WriteString("tools:\n")
	sb.WriteString("  max_output_size: 102400     # Max bytes of tool output (100 KB)\n")
	sb.WriteString("  scrub_output: true          # Remove sensitive data from output\n")
	sb.WriteString("  exec:\n")
	sb.WriteString("    sandbox: none             # none | docker (sandbox mode for commands)\n")
	sb.WriteString("    timeout: 30s              # Max command execution time\n")
	sb.WriteString("  browser:\n")
	sb.WriteString("    enabled: true             # Enable web browser automation\n")
	sb.WriteString("    headless: true            # Run browser without visible window\n")
	sb.WriteString("    timeout: 30s              # Page load timeout\n")
	sb.WriteString("\n")

	sb.WriteString("# ── Channel Adapters ──────────────────────────────────────────────\n")
	sb.WriteString("channels:\n")
	sb.WriteString("  allow_all: true             # Allow all configured channels\n")
	if enableTelegram {
		sb.WriteString("  telegram:\n")
		sb.WriteString("    token_env: TELEGRAM_BOT_TOKEN\n")
	}
	if enableDiscord {
		sb.WriteString("  discord:\n")
		sb.WriteString("    token_env: DISCORD_BOT_TOKEN\n")
		sb.WriteString("    app_id_env: DISCORD_APP_ID\n")
	}
	if enableWhatsApp {
		sb.WriteString("  whatsapp:\n")
		sb.WriteString("    enabled: true\n")
		sb.WriteString("    # Uses QR login via WhatsApp Linked Devices\n")
	}
	if enableWebChat {
		sb.WriteString("  # WebChat UI enabled at http://127.0.0.1:" + portStr + "\n")
	}
	sb.WriteString("\n")

	sb.WriteString("# ── Logging ────────────────────────────────────────────────────────\n")
	sb.WriteString("logging:\n")
	sb.WriteString("  level: info                 # debug | info | warn | error\n")
	sb.WriteString("  output: ~/.openclio/openclio.log\n")

	// Write config file
	if err := os.WriteFile(configPath, []byte(sb.String()), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing config: %v\n", err)
		os.Exit(1)
	}

	// When user selects Ollama, pull the embedding model so semantic search works
	if provider == "ollama" {
		embedModel := "nomic-embed-text"
		fmt.Printf("Pulling embedding model (%s) for semantic search...\n", embedModel)
		if cmd := exec.Command("ollama", "pull", embedModel); cmd.Run() != nil {
			fmt.Printf("Note: run 'ollama pull %s' manually if semantic search fails.\n", embedModel)
		}
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// STEP 6: Customize Identity with Personality
	// ═══════════════════════════════════════════════════════════════════════════
	identityPath := filepath.Join(dataDir, "identity.md")
	if content, err := os.ReadFile(identityPath); err == nil {
		contentStr := string(content)
		
		// Add personality customization section at the end
		personalityNote := fmt.Sprintf("\n\n## 🎨 Personality Customization\n\n"+
			"<!-- Added during initialization -->\n\n"+
			"**Assistant Name:** %s\n\n"+
			"**Personality Style:** %s\n\n"+
			"**Key Traits:**\n", assistantName, traits[0])
		
		for i := 1; i < len(traits); i++ {
			personalityNote += fmt.Sprintf("- %s\n", traits[i])
		}
		
		personalityNote += fmt.Sprintf("\n**Response Style:** %s\n", responseStyleLabels[responseStyle])
		
		if userStack != "" {
			personalityNote += fmt.Sprintf("\n**User Stack:** %s\n", userStack)
		}
		
		// Append to the file
		contentStr = contentStr + personalityNote
		_ = os.WriteFile(identityPath, []byte(contentStr), 0600)
	}

	// ═══════════════════════════════════════════════════════════════════════════
	// Success Message
	// ═══════════════════════════════════════════════════════════════════════════
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║  ✅ Setup Complete!                                              ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Show what was created
	fmt.Printf("📁 Configuration directory: %s\n", dataDir)
	fmt.Println()
	fmt.Println("📄 Files created:")
	fmt.Println("   • config.yaml          — Your main configuration")
	fmt.Println("   • identity.md          — " + assistantName + "'s personality & values")
	fmt.Println("   • user.md              — Your profile and preferences")
	fmt.Println("   • memory.md            — Long-term memory structure")
	fmt.Println("   • PHILOSOPHY.md        — Project vision & principles")
	fmt.Println("   • AGENTS_REFERENCE.md  — Developer reference guide")
	fmt.Println("   • skills/              — Default skill templates")
	fmt.Println()

	// Show API key instructions if needed
	if apiKeyEnv != "" {
		fmt.Println("🔑 Next: Set your API key")
		fmt.Printf("   export %s=<your-api-key>\n", apiKeyEnv)
		if apiKeyHint != "" {
			fmt.Printf("   Get one at: %s\n", apiKeyHint)
		}
		fmt.Println()
	}

	// Show channel setup instructions
	if enableTelegram || enableDiscord || enableWhatsApp {
		fmt.Println("📡 Channel Setup:")
		if enableTelegram {
			fmt.Println("   Telegram: Set TELEGRAM_BOT_TOKEN=<token>")
			fmt.Println("             Get a token from @BotFather on Telegram")
		}
		if enableDiscord {
			fmt.Println("   Discord:  Set DISCORD_BOT_TOKEN=<token>")
			fmt.Println("             Get a token at https://discord.com/developers/applications")
		}
		if enableWhatsApp {
			fmt.Println("   WhatsApp: No token required (QR login)")
			fmt.Println("             Start `openclio serve` and scan the QR code in the web UI")
		}
		fmt.Println()
	}

	// Quick start guide
	fmt.Println("🚀 Quick Start:")
	fmt.Println("   openclio chat          — Start chatting in terminal")
	fmt.Println("   openclio serve         — Start web UI + all channels")
	fmt.Println()
	fmt.Printf("✨ %s is ready with full personality and memory!\n", assistantName)
	fmt.Println()
	fmt.Println("💡 Pro tip: Edit ~/.openclio/identity.md to customize your agent further.")
	fmt.Println()
}

// ── Prompt helpers ───────────────────────────────────────────────────────────

var initReader = bufio.NewReader(os.Stdin)

func promptString(label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("📝 %s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("📝 %s: ", label)
	}
	line, _ := initReader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

func promptConfirm(label string, defaultYes bool) bool {
	hint := "y/N"
	if defaultYes {
		hint = "Y/n"
	}
	fmt.Printf("📝 %s [%s]: ", label, hint)
	line, _ := initReader.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}

func promptChoice(label string, choices []string, defaultChoice string) string {
	allowed := strings.Join(choices, "/")
	for {
		if defaultChoice != "" {
			fmt.Printf("📝 %s [%s]: ", label, defaultChoice)
		} else {
			fmt.Printf("📝 %s [%s]: ", label, allowed)
		}

		line, _ := initReader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			if defaultChoice != "" {
				return defaultChoice
			}
			fmt.Printf("   Please choose one of: %s\n", allowed)
			continue
		}
		for _, c := range choices {
			if line == c {
				return c
			}
		}
		fmt.Printf("   Invalid choice '%s'. Enter one of: %s\n", line, allowed)
	}
}

func promptMultiline(prefix string) string {
	var lines []string
	for {
		fmt.Print(prefix)
		line, _ := initReader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) == "" {
			break
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
