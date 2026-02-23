package tools

import (
	"regexp"
)

// passwordPatterns are patterns that commonly appear in tool output that may
// contain credential or secret values. Matched values are replaced with [REDACTED].
//
// This provides Layer 5 (Data Privacy) and Layer 8 (Prompt Injection Defense)
// protection: credentials scraped by web tools or printed by exec commands
// never reach the agent context or get stored in message history.
var passwordPatterns = []*regexp.Regexp{
	// password= / PASSWORD= in any casing, followed by a value
	regexp.MustCompile(`(?i)(password\s*=\s*|password:\s*)\S+`),
	// secret= / SECRET=
	regexp.MustCompile(`(?i)(secret\s*=\s*|secret:\s*)\S+`),
	// token= in env/query string form
	regexp.MustCompile(`(?i)(token\s*=\s*)\S+`),
	// private_key, api_key, access_key in env style
	regexp.MustCompile(`(?i)((private_key|api_key|access_key|secret_key|auth_key)\s*=\s*)\S+`),
	// AWS-style keys
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	// OpenAI / Anthropic / xAI tokens in output
	regexp.MustCompile(`sk-[a-zA-Z0-9\-_]{20,}`),
	regexp.MustCompile(`sk-ant-[a-zA-Z0-9\-_]{20,}`),
	regexp.MustCompile(`xai-[a-zA-Z0-9\-_]{20,}`),
	// Authorization: Bearer <token>
	regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)\S+`),
	// Private key PEM blocks
	regexp.MustCompile(`-----BEGIN [A-Z ]+PRIVATE KEY-----`),
}

// ScrubToolOutput redacts credential-like patterns from tool output before
// it is passed to the LLM or stored in message history.
// This is a best-effort defence; it does not guarantee all secrets are removed.
func ScrubToolOutput(s string) string {
	out, _ := ScrubToolOutputWithCount(s)
	return out
}

// ScrubToolOutputWithCount redacts sensitive content and returns how many
// replacements were made.
func ScrubToolOutputWithCount(s string) (string, int) {
	redactions := 0
	for _, re := range passwordPatterns {
		s = re.ReplaceAllStringFunc(s, func(match string) string {
			redactions++
			// Find the last captured group (the prefix, e.g. "password=")
			// and keep it; redact the rest.
			subs := re.FindStringSubmatch(match)
			if len(subs) > 1 {
				// Keep the last non-empty capture group as prefix
				for i := len(subs) - 1; i >= 1; i-- {
					if subs[i] != "" {
						return subs[i] + "[REDACTED]"
					}
				}
			}
			return "[REDACTED]"
		})
	}
	return s, redactions
}
