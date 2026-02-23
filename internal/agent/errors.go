// Package agent — error codes for programmatic handling.
//
// Error codes follow the pattern E<component><seq>:
//
//	E1xxx — LLM provider errors
//	E2xxx — tool execution errors
//	E3xxx — storage errors
package agent

import "fmt"

// AgentError is a structured error with a machine-readable code and a
// human-readable, actionable message.
type AgentError struct {
	Code string // e.g. "E1001"
	Msg  string // human-readable description

	// Cause is the underlying error, if any.
	Cause error
}

func (e *AgentError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Msg, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Msg)
}

func (e *AgentError) Unwrap() error { return e.Cause }

// Wrap attaches an underlying cause to an AgentError, returning a new value.
func (e *AgentError) Wrap(cause error) *AgentError {
	return &AgentError{Code: e.Code, Msg: e.Msg, Cause: cause}
}

// Sentinel errors — use .Wrap(cause) to attach context.
var (
	// Provider errors (E1xxx)
	ErrProviderTimeout     = &AgentError{Code: "E1001", Msg: "LLM provider timed out — check network or increase timeout"}
	ErrProviderRateLimit   = &AgentError{Code: "E1002", Msg: "LLM rate limit hit — slow down or upgrade your plan"}
	ErrProviderUnavailable = &AgentError{Code: "E1003", Msg: "LLM provider unavailable — all retry attempts failed"}
	ErrAllProvidersFailed  = &AgentError{Code: "E1004", Msg: "all configured providers failed — check your API keys and network"}
	ErrContextOverflow     = &AgentError{Code: "E1005", Msg: "context limit reached — try /reset to start a fresh session"}

	// Tool errors (E2xxx)
	ErrToolExecution = &AgentError{Code: "E2001", Msg: "tool execution failed"}
	ErrToolBlocked   = &AgentError{Code: "E2002", Msg: "tool call blocked by security policy"}

	// Storage errors (E3xxx)
	ErrStorageWrite   = &AgentError{Code: "E3001", Msg: "storage write failed — check disk space and permissions"}
	ErrStorageLocked  = &AgentError{Code: "E3002", Msg: "database is locked — another process may be accessing it"}
	ErrStorageCorrupt = &AgentError{Code: "E3003", Msg: "database appears corrupted — run: agent wipe --data-only to reset"}
)
