package persistence

import "testing"

func TestNewMigratorAndModels(t *testing.T) {
	m := NewMigrator()
	models := m.Models()
	if len(models) == 0 {
		t.Fatal("Models() returned no entries; AutoMigrate would skip every aggregate")
	}
	for i, mdl := range models {
		if mdl == nil {
			t.Errorf("models[%d] is nil", i)
		}
	}
}

func TestNonNilJSON(t *testing.T) {
	if string(nonNilJSON(nil)) != "[]" {
		t.Error("nil should map to []")
	}
	if string(nonNilJSON([]byte{})) != "[]" {
		t.Error("empty slice should map to []")
	}
	in := []byte(`{"x":1}`)
	if string(nonNilJSON(in)) != `{"x":1}` {
		t.Error("non-empty input should pass through")
	}
}

func TestOrEmpty(t *testing.T) {
	if got := orEmpty[int](nil); got == nil || len(got) != 0 {
		t.Errorf("nil → empty slice expected, got %v", got)
	}
	in := []string{"a"}
	if got := orEmpty(in); len(got) != 1 || got[0] != "a" {
		t.Errorf("non-nil should pass through, got %v", got)
	}
}
