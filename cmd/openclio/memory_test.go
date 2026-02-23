package main

import (
	"os"
	"strings"
	"testing"

	"github.com/openclio/openclio/internal/kg"
	"github.com/openclio/openclio/internal/storage"
)

func seedMemoryTestData(t *testing.T, db *storage.DB) {
	t.Helper()
	store := storage.NewKnowledgeGraphStore(db)
	if err := store.IngestExtracted(1001, []kg.Entity{
		{Type: "company", Name: "Acme", Confidence: 0.9},
		{Type: "person", Name: "Sarah", Confidence: 0.8},
	}, []kg.Relation{{From: "Sarah", Relation: "works_at", To: "Acme"}}); err != nil {
		t.Fatalf("IngestExtracted: %v", err)
	}
}

func TestRunMemoryListAndSearch(t *testing.T) {
	db := setupCLIHistoryUndoDB(t)
	defer db.Close()
	seedMemoryTestData(t, db)

	listOut := captureStdout(t, func() {
		runMemory(db, []string{"list", "--limit", "10"})
	})
	if !strings.Contains(listOut, "Acme") || !strings.Contains(listOut, "Sarah") {
		t.Fatalf("memory list output missing expected entities:\n%s", listOut)
	}

	searchOut := captureStdout(t, func() {
		runMemory(db, []string{"search", "Acme"})
	})
	if !strings.Contains(searchOut, "Acme") {
		t.Fatalf("memory search output missing Acme:\n%s", searchOut)
	}
}

func TestRunMemoryEdit(t *testing.T) {
	db := setupCLIHistoryUndoDB(t)
	defer db.Close()
	seedMemoryTestData(t, db)

	prevEditor := os.Getenv("EDITOR")
	defer os.Setenv("EDITOR", prevEditor)
	if err := os.Setenv("EDITOR", "cat"); err != nil {
		t.Fatalf("set EDITOR: %v", err)
	}

	out := captureStdout(t, func() {
		runMemory(db, []string{"edit"})
	})
	if !strings.Contains(out, "Updated") {
		t.Fatalf("memory edit output missing update summary:\n%s", out)
	}
}
