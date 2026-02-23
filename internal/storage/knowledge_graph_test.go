package storage

import (
	"testing"
	"time"

	"github.com/openclio/openclio/internal/kg"
)

func TestKnowledgeGraphStore_IngestExtracted(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewKnowledgeGraphStore(db)
	entities := []kg.Entity{
		{Type: "project", Name: "Acme Dashboard", Confidence: 0.9},
		{Type: "person", Name: "Sarah", Confidence: 0.8},
		{Type: "technology", Name: "React", Confidence: 0.7},
	}
	relations := []kg.Relation{
		{From: "Sarah", Relation: "collaborator_on", To: "Acme Dashboard"},
		{From: "Acme Dashboard", Relation: "uses", To: "React"},
	}
	if err := store.IngestExtracted(123, entities, relations); err != nil {
		t.Fatalf("IngestExtracted: %v", err)
	}

	nodes, err := store.ListNodes(20)
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) < 3 {
		t.Fatalf("expected at least 3 nodes, got %d", len(nodes))
	}

	edges, err := store.ListEdges(20)
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}

	// ingesting the same graph again should not duplicate unique edges.
	if err := store.IngestExtracted(124, entities, relations); err != nil {
		t.Fatalf("second IngestExtracted: %v", err)
	}
	edges, err = store.ListEdges(20)
	if err != nil {
		t.Fatalf("ListEdges after second ingest: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected deduped edges count=2, got %d", len(edges))
	}
}

func TestMessageStoreExtractsKnowledgeGraphOnInsert(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	sessionStore := NewSessionStore(db)
	session, err := sessionStore.Create("cli", "user")
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	kgStore := NewKnowledgeGraphStore(db)
	msgStore := NewMessageStore(db)
	msgStore.SetKnowledgeGraphStore(kgStore)

	if _, err := msgStore.Insert(session.ID, "user", "I'm working on Acme Dashboard with Sarah using React.", 20); err != nil {
		t.Fatalf("Insert message: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		nodes, err := kgStore.ListNodes(20)
		if err != nil {
			t.Fatalf("ListNodes: %v", err)
		}
		if len(nodes) >= 3 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	nodes, _ := kgStore.ListNodes(20)
	t.Fatalf("expected extracted knowledge graph nodes after insert, got %d (%+v)", len(nodes), nodes)
}

func TestKnowledgeGraphStore_SearchUpdateDelete(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewKnowledgeGraphStore(db)
	if err := store.IngestExtracted(500, []kg.Entity{
		{Type: "company", Name: "Acme Inc", Confidence: 0.7},
		{Type: "person", Name: "Sarah", Confidence: 0.8},
	}, nil); err != nil {
		t.Fatalf("IngestExtracted: %v", err)
	}

	found, err := store.SearchNodes("acme", "", 10)
	if err != nil {
		t.Fatalf("SearchNodes by name: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("expected search results for acme")
	}

	typed, err := store.SearchNodes("", "company", 10)
	if err != nil {
		t.Fatalf("SearchNodes by type: %v", err)
	}
	if len(typed) == 0 {
		t.Fatal("expected typed search results")
	}

	target := found[0]
	if err := store.UpdateNode(target.ID, "organization", "Acme Corp", 0.95); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}

	updated, err := store.SearchNodes("corp", "", 10)
	if err != nil {
		t.Fatalf("SearchNodes after update: %v", err)
	}
	if len(updated) == 0 {
		t.Fatal("expected updated node to be searchable by new name")
	}

	if err := store.DeleteNode(target.ID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if err := store.DeleteNode(target.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound on second delete, got %v", err)
	}
}
