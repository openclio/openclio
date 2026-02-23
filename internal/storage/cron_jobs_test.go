package storage

import "testing"

func TestCronJobStoreCRUD(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewCronJobStore(db)

	created, err := store.Create(CronJob{
		Name:        "daily-summary",
		Schedule:    "0 9 * * *",
		Trigger:     "",
		Prompt:      "Summarize updates",
		Channel:     "webchat",
		SessionMode: "shared",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 {
		t.Fatalf("expected non-zero ID")
	}
	if created.TimeoutSec != 300 {
		t.Fatalf("expected default timeout 300s, got %d", created.TimeoutSec)
	}

	jobs, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}

	if err := store.UpdateByName("daily-summary", CronJob{
		Schedule:    "0 10 * * *",
		Trigger:     "every 6 hours",
		Prompt:      "Summarize latest updates",
		Channel:     "telegram",
		SessionMode: "isolated",
		TimeoutSec:  45,
	}); err != nil {
		t.Fatalf("UpdateByName: %v", err)
	}

	got, err := store.GetByName("daily-summary")
	if err != nil {
		t.Fatalf("GetByName after update: %v", err)
	}
	if got.Schedule != "0 10 * * *" || got.Channel != "telegram" || got.SessionMode != "isolated" || got.TimeoutSec != 45 {
		t.Fatalf("unexpected updated job: %+v", got)
	}
	if got.Trigger != "every 6 hours" {
		t.Fatalf("expected trigger persisted, got %q", got.Trigger)
	}

	if err := store.SetEnabled("daily-summary", false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	got, err = store.GetByName("daily-summary")
	if err != nil {
		t.Fatalf("GetByName after disable: %v", err)
	}
	if got.Enabled {
		t.Fatalf("expected Enabled=false after disable")
	}

	if err := store.DeleteByName("daily-summary"); err != nil {
		t.Fatalf("DeleteByName: %v", err)
	}
	if _, err := store.GetByName("daily-summary"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
