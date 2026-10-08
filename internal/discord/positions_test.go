package discord

import (
	"slices"
	"testing"
)

func apply(current []Positioned, updates []PositionUpdate) []Positioned {
	out := slices.Clone(current)
	for _, u := range updates {
		for i := range out {
			if out[i].ID == u.ID {
				out[i].Position = u.Position
			}
		}
	}
	return out
}

func TestReorder(t *testing.T) {
	tests := []struct {
		name        string
		current     []Positioned
		desired     []string
		wantUpdates int
	}{
		{
			name:        "already ordered",
			current:     []Positioned{{"1", 1}, {"2", 2}, {"3", 3}},
			desired:     []string{"1", "2", "3"},
			wantUpdates: 0,
		},
		{
			name:        "reverse",
			current:     []Positioned{{"1", 1}, {"2", 2}, {"3", 3}},
			desired:     []string{"3", "2", "1"},
			wantUpdates: 2,
		},
		{
			name:        "ties are broken",
			current:     []Positioned{{"1", 0}, {"2", 0}, {"3", 0}},
			desired:     []string{"3", "1", "2"},
			wantUpdates: 2,
		},
		{
			name:        "subset keeps unlisted in place",
			current:     []Positioned{{"1", 1}, {"2", 2}, {"3", 3}, {"4", 4}},
			desired:     []string{"3", "1"},
			wantUpdates: 2,
		},
		{
			name:        "unknown IDs are ignored",
			current:     []Positioned{{"1", 1}, {"2", 2}},
			desired:     []string{"2", "9", "1"},
			wantUpdates: 2,
		},
		{
			name:        "empty",
			current:     nil,
			desired:     nil,
			wantUpdates: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updates := Reorder(tt.current, tt.desired)
			if len(updates) != tt.wantUpdates {
				t.Errorf("got %d updates (%v), want %d", len(updates), updates, tt.wantUpdates)
			}
			after := apply(tt.current, updates)
			var known []string
			for _, id := range tt.desired {
				if slices.ContainsFunc(tt.current, func(p Positioned) bool { return p.ID == id }) {
					known = append(known, id)
				}
			}
			if got := OrderOf(after, tt.desired); !slices.Equal(got, known) {
				t.Errorf("order after updates = %v, want %v", got, known)
			}
			if again := Reorder(after, tt.desired); len(again) != 0 {
				t.Errorf("reorder is not idempotent: %v", again)
			}
		})
	}
}

func TestSortByPositionTiesBySnowflake(t *testing.T) {
	items := []Positioned{{"300", 1}, {"1000", 1}, {"20", 1}, {"5", 0}}
	SortByPosition(items)
	var ids []string
	for _, p := range items {
		ids = append(ids, p.ID)
	}
	if want := []string{"5", "20", "300", "1000"}; !slices.Equal(ids, want) {
		t.Errorf("order = %v, want %v", ids, want)
	}
}
