package storage

import "testing"

func TestPrivacyStore_RecordAndAggregate(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewPrivacyStore(db)
	if err := store.RecordRedaction("secret", 2); err != nil {
		t.Fatalf("RecordRedaction(secret,2): %v", err)
	}
	if err := store.RecordRedaction("secret", 3); err != nil {
		t.Fatalf("RecordRedaction(secret,3): %v", err)
	}
	if err := store.RecordRedaction("email", 5); err != nil {
		t.Fatalf("RecordRedaction(email,5): %v", err)
	}
	// no-op path
	if err := store.RecordRedaction("secret", 0); err != nil {
		t.Fatalf("RecordRedaction(secret,0): %v", err)
	}

	secrets, err := store.TotalByCategory("secret")
	if err != nil {
		t.Fatalf("TotalByCategory(secret): %v", err)
	}
	if secrets != 5 {
		t.Fatalf("expected secret total 5, got %d", secrets)
	}

	emails, err := store.TotalByCategory("email")
	if err != nil {
		t.Fatalf("TotalByCategory(email): %v", err)
	}
	if emails != 5 {
		t.Fatalf("expected email total 5, got %d", emails)
	}

	missing, err := store.TotalByCategory("missing")
	if err != nil {
		t.Fatalf("TotalByCategory(missing): %v", err)
	}
	if missing != 0 {
		t.Fatalf("expected missing total 0, got %d", missing)
	}
}
