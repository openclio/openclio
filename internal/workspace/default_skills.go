package workspace

import (
	"fmt"
	"os"
	"strings"
)

type defaultSkill struct {
	Name    string
	Content string
}

var bundledDefaultSkills = []defaultSkill{
	{
		Name: "code-review",
		Content: `# Code Review
Perform a focused production-grade code review.

Checklist:
1. Correctness and edge cases.
2. Error handling and failure paths.
3. Concurrency safety and resource leaks.
4. Security risks and unsafe assumptions.
5. Missing tests for changed behavior.

Response format:
- Findings first, ordered by severity.
- Include file paths and concrete fix suggestions.
`,
	},
	{
		Name: "security-audit",
		Content: `# Security Audit
Audit code and config for practical security risks.

Priorities:
1. Authn/authz gaps.
2. Secret handling and data exposure.
3. Injection vectors (SQL, command, template, prompt).
4. Unsafe defaults and insecure transport/storage.
5. Logging of sensitive data.

Response format:
- Risk, impact, and direct mitigation steps.
`,
	},
	{
		Name: "bug-triage",
		Content: `# Bug Triage
Triage incoming bugs quickly and precisely.

Workflow:
1. Reproduce or isolate likely failure path.
2. Classify severity and user impact.
3. Identify probable root cause.
4. Propose minimal safe fix.
5. Add validation steps and regression tests.

Response format:
- Summary, root cause, fix plan, verification checklist.
`,
	},
	{
		Name: "release-checklist",
		Content: `# Release Checklist
Prepare a production release with disciplined checks.

Checklist:
1. Build and lint/vet pass.
2. Migrations reviewed and rollback considered.
3. Config/environment changes documented.
4. Observability and alerting verified.
5. Changelog and version notes updated.
6. Post-release smoke checks defined.

Output:
- A go/no-go checklist with blockers clearly marked.
`,
	},
	{
		Name: "perf-profiling",
		Content: `# Performance Profiling
Investigate runtime performance bottlenecks.

Workflow:
1. Define baseline latency/throughput and workload.
2. Locate hotspots using profiling evidence.
3. Propose the smallest high-impact optimization.
4. Measure before/after numbers.
5. Document tradeoffs and rollback path.

Output:
- Bottleneck evidence, fix, and measured result.
`,
	},
	{
		Name: "docs-writer",
		Content: `# Docs Writer
Write concise, accurate technical documentation.

Rules:
1. Start with outcome and prerequisites.
2. Use copy-pasteable commands/examples.
3. Keep structure clear and skimmable.
4. Call out pitfalls and troubleshooting.
5. Match repository terminology and tone.

Output:
- Ready-to-commit docs with minimal ambiguity.
`,
	},
	{
		Name: "migration-planner",
		Content: `# Migration Planner
Plan schema/data or service migrations safely.

Workflow:
1. Define source/target states and invariants.
2. Choose rollout strategy (expand-contract preferred).
3. Plan backfill, verification, and cutover.
4. Define rollback criteria and procedure.
5. Estimate operational risk and owner actions.

Output:
- Phase-by-phase migration plan with checkpoints.
`,
	},
	{
		Name: "incident-response",
		Content: `# Incident Response
Handle production incidents with clarity and speed.

Workflow:
1. Confirm blast radius and customer impact.
2. Stabilize service first.
3. Collect timeline and key telemetry.
4. Identify trigger and contributing factors.
5. Define immediate remediation and follow-ups.

Output:
- Incident summary, timeline, root cause, action items.
`,
	},
}

// SeedDefaultSkills installs bundled default skills if missing.
// Existing enabled or disabled skills are never overwritten.
func SeedDefaultSkills(dataDir string) error {
	if strings.TrimSpace(dataDir) == "" {
		return fmt.Errorf("data directory is required")
	}

	for _, skill := range bundledDefaultSkills {
		enabledPath, disabledPath := skillFilePaths(dataDir, skill.Name)

		if _, err := os.Stat(enabledPath); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("checking skill %q: %w", skill.Name, err)
		}

		if _, err := os.Stat(disabledPath); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("checking disabled skill %q: %w", skill.Name, err)
		}

		if err := InstallSkill(dataDir, skill.Name, skill.Content, true); err != nil {
			return fmt.Errorf("installing default skill %q: %w", skill.Name, err)
		}
	}
	return nil
}
