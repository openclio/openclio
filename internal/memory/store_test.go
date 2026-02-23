package memory

import (
	"context"
	"testing"
)

func TestStoreAddGetSearchDelete(t *testing.T) {
	ctx := context.Background()
	s, err := NewStore(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	id, err := s.Add(ctx, "Alice likes cats", map[string]interface{}{"topic": "pets"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if id == "" {
		t.Fatalf("empty id")
	}

	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.Content != "Alice likes cats" {
		t.Fatalf("unexpected get: %#v", got)
	}

	// search
	results, err := s.Search(ctx, "cats", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected search results")
	}

	// delete
	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got2, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got2 != nil {
		t.Fatalf("expected nil after delete, got %#v", got2)
	}
}
