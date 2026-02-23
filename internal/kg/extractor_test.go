package kg

import "testing"

func TestExtractEntitiesAndRelations(t *testing.T) {
	text := "I am working on Atlas Engine with Sarah Connor, deadline is March 3, 2026. We are using Go and Docker."
	entities, relations := Extract(text)

	if len(entities) == 0 {
		t.Fatal("expected entities to be extracted")
	}
	if len(relations) == 0 {
		t.Fatal("expected relations to be extracted")
	}

	entityTypes := map[string]string{}
	for _, e := range entities {
		entityTypes[e.Name] = e.Type
	}

	if got := entityTypes["Atlas Engine"]; got != "project" {
		t.Fatalf("expected Atlas Engine project entity, got %q", got)
	}
	if got := entityTypes["Sarah Connor"]; got != "person" {
		t.Fatalf("expected Sarah Connor person entity, got %q", got)
	}
	if got := entityTypes["March 3, 2026"]; got != "deadline" {
		t.Fatalf("expected deadline entity, got %q", got)
	}
	if got := entityTypes["Go"]; got != "technology" {
		t.Fatalf("expected Go technology entity, got %q", got)
	}
	if got := entityTypes["Docker"]; got != "technology" {
		t.Fatalf("expected Docker technology entity, got %q", got)
	}

	hasRelation := func(from, rel, to string) bool {
		for _, r := range relations {
			if r.From == from && r.Relation == rel && r.To == to {
				return true
			}
		}
		return false
	}

	if !hasRelation("Sarah Connor", "collaborator_on", "Atlas Engine") {
		t.Fatalf("expected collaborator relation in %+v", relations)
	}
	if !hasRelation("Atlas Engine", "uses", "Go") {
		t.Fatalf("expected uses(Go) relation in %+v", relations)
	}
	if !hasRelation("Atlas Engine", "uses", "Docker") {
		t.Fatalf("expected uses(Docker) relation in %+v", relations)
	}
	if !hasRelation("Atlas Engine", "deadline", "March 3, 2026") {
		t.Fatalf("expected deadline relation in %+v", relations)
	}
}

func TestExtractDeduplicatesTechnologyEntities(t *testing.T) {
	text := "Go is great. I use go daily. Also GO works for backend."
	entities, _ := Extract(text)

	countGo := 0
	for _, e := range entities {
		if e.Name == "Go" && e.Type == "technology" {
			countGo++
		}
	}
	if countGo != 1 {
		t.Fatalf("expected exactly one Go entity, got %d (%+v)", countGo, entities)
	}
}

func TestExtractEmptyInput(t *testing.T) {
	entities, relations := Extract("   ")
	if len(entities) != 0 {
		t.Fatalf("expected no entities for empty input, got %+v", entities)
	}
	if len(relations) != 0 {
		t.Fatalf("expected no relations for empty input, got %+v", relations)
	}
}
