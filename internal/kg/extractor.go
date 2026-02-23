package kg

import (
	"regexp"
	"strings"
)

// Entity is one extracted knowledge-graph entity.
type Entity struct {
	Type       string
	Name       string
	Confidence float64
}

// Relation is one inferred edge between extracted entities.
type Relation struct {
	From     string
	Relation string
	To       string
}

var (
	projectPattern  = regexp.MustCompile(`\b(?i:working on|project)\s+([A-Z][A-Za-z0-9._-]*(?:\s+[A-Z][A-Za-z0-9._-]*){0,4})`)
	personPattern   = regexp.MustCompile(`\b(?i:with)\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)`)
	deadlinePattern = regexp.MustCompile(`\b(?i:deadline|due)\s*(?:(?i:is|on)\s+)?([A-Za-z]+ \d{1,2}(?:, \d{4})?)`)
)

// Extract returns entities and relations inferred from one message.
func Extract(text string) ([]Entity, []Relation) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, nil
	}

	entityByKey := map[string]Entity{}
	addEntity := func(kind, name string, confidence float64) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if existing, ok := entityByKey[key]; ok {
			if confidence > existing.Confidence {
				existing.Confidence = confidence
				entityByKey[key] = existing
			}
			return
		}
		entityByKey[key] = Entity{Type: kind, Name: name, Confidence: confidence}
	}

	lower := strings.ToLower(trimmed)
	for _, match := range projectPattern.FindAllStringSubmatch(trimmed, -1) {
		if len(match) > 1 {
			addEntity("project", match[1], 0.85)
		}
	}
	for _, match := range personPattern.FindAllStringSubmatch(trimmed, -1) {
		if len(match) > 1 {
			addEntity("person", match[1], 0.8)
		}
	}
	for _, match := range deadlinePattern.FindAllStringSubmatch(trimmed, -1) {
		if len(match) > 1 {
			addEntity("deadline", match[1], 0.75)
		}
	}

	techKeywords := map[string]string{
		"go":         "Go",
		"python":     "Python",
		"typescript": "TypeScript",
		"react":      "React",
		"docker":     "Docker",
		"kubernetes": "Kubernetes",
		"postgres":   "PostgreSQL",
		"sqlite":     "SQLite",
		"openai":     "OpenAI",
		"anthropic":  "Anthropic",
		"ollama":     "Ollama",
	}
	for needle, canonical := range techKeywords {
		if strings.Contains(lower, needle) {
			addEntity("technology", canonical, 0.7)
		}
	}

	entities := make([]Entity, 0, len(entityByKey))
	var projects []string
	var people []string
	var technologies []string
	var deadlines []string
	for _, entity := range entityByKey {
		entities = append(entities, entity)
		switch entity.Type {
		case "project":
			projects = append(projects, entity.Name)
		case "person":
			people = append(people, entity.Name)
		case "technology":
			technologies = append(technologies, entity.Name)
		case "deadline":
			deadlines = append(deadlines, entity.Name)
		}
	}

	relations := make([]Relation, 0)
	for _, project := range projects {
		for _, person := range people {
			relations = append(relations, Relation{
				From:     person,
				Relation: "collaborator_on",
				To:       project,
			})
		}
		for _, tech := range technologies {
			relations = append(relations, Relation{
				From:     project,
				Relation: "uses",
				To:       tech,
			})
		}
		for _, deadline := range deadlines {
			relations = append(relations, Relation{
				From:     project,
				Relation: "deadline",
				To:       deadline,
			})
		}
	}

	return entities, relations
}
