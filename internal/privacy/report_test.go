package privacy

import (
	"testing"

	"github.com/openclio/openclio/internal/cost"
	"github.com/openclio/openclio/internal/storage"
)

func setupReportTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db := setupStorageTestDB(t)
	return db
}

func setupStorageTestDB(t *testing.T) *storage.DB {
	t.Helper()
	// Reuse storage test helper semantics locally to keep this package decoupled.
	dbPath := t.TempDir() + "/privacy-report.db"
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	return db
}

func TestBuildReportIncludesCostAndPrivacyTotals(t *testing.T) {
	db := setupReportTestDB(t)
	defer db.Close()

	sessions := storage.NewSessionStore(db)
	session, err := sessions.Create("test", "user")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	tracker := cost.NewTracker(db)
	if err := tracker.Record(session.ID, "anthropic", "claude-sonnet-4-20250514", 1000, 500); err != nil {
		t.Fatalf("record anthropic usage: %v", err)
	}
	if err := tracker.Record(session.ID, "ollama", "llama3.1", 200, 100); err != nil {
		t.Fatalf("record ollama usage: %v", err)
	}

	privacyStore := storage.NewPrivacyStore(db)
	if err := privacyStore.RecordRedaction("secret", 4); err != nil {
		t.Fatalf("record redaction: %v", err)
	}

	report, err := BuildReport(tracker, privacyStore, true, "all")
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if report.Privacy.SecretsRedacted != 4 {
		t.Fatalf("expected 4 redactions, got %d", report.Privacy.SecretsRedacted)
	}
	if report.Totals.Calls != 2 {
		t.Fatalf("expected 2 calls, got %d", report.Totals.Calls)
	}
	if len(report.Providers) != 2 {
		t.Fatalf("expected 2 provider rows, got %d", len(report.Providers))
	}

	var sawCloud bool
	var sawLocal bool
	for _, p := range report.Providers {
		if p.Provider == "anthropic" && p.Privacy == "cloud" {
			sawCloud = true
		}
		if p.Provider == "ollama" && p.Privacy == "local" {
			sawLocal = true
		}
	}
	if !sawCloud || !sawLocal {
		t.Fatalf("expected anthropic=cloud and ollama=local, got %#v", report.Providers)
	}
}

func TestBuildReportHandlesNilStores(t *testing.T) {
	report, err := BuildReport(nil, nil, false, "")
	if err != nil {
		t.Fatalf("BuildReport(nil,nil): %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.Period != "all" {
		t.Fatalf("expected default period all, got %q", report.Period)
	}
	if report.Privacy.SecretsRedacted != 0 {
		t.Fatalf("expected 0 redactions, got %d", report.Privacy.SecretsRedacted)
	}
}
