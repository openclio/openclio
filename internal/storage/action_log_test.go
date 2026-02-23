package storage

import "testing"

func TestActionLogStore_WriteAndExecEntries(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewActionLogStore(db)
	if err := store.RecordWriteFile("/tmp/demo.txt", true, "before", "after", true, ""); err != nil {
		t.Fatalf("RecordWriteFile: %v", err)
	}
	if err := store.RecordExec("echo hi", "/tmp", "hi\n", false, "exit code 1"); err != nil {
		t.Fatalf("RecordExec: %v", err)
	}

	entries, err := store.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	var sawWrite bool
	var sawExec bool
	for _, e := range entries {
		switch e.ToolName {
		case "write_file":
			sawWrite = true
			if !e.BeforeExists || e.BeforeContent != "before" || e.AfterContent != "after" || !e.Success {
				t.Fatalf("unexpected write entry: %+v", e)
			}
		case "exec":
			sawExec = true
			if e.Command != "echo hi" || e.WorkDir != "/tmp" || e.Success || e.Error == "" {
				t.Fatalf("unexpected exec entry: %+v", e)
			}
		}
	}
	if !sawWrite || !sawExec {
		t.Fatalf("expected write and exec entries, got %+v", entries)
	}
}

func TestActionLogStore_GetNotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewActionLogStore(db)
	_, err := store.Get(99999)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
