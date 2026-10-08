package discord

import (
	"math/rand/v2"
	"slices"
	"strconv"
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

func displayOrder(items []Positioned) []string {
	sorted := slices.Clone(items)
	SortByPosition(sorted)
	ids := make([]string, len(sorted))
	for i, p := range sorted {
		ids[i] = p.ID
	}
	return ids
}

// wantOrder is the display order after reordering: the current order with the
// slots of the listed items filled in the desired order.
func wantOrder(current []Positioned, desired []string) []string {
	ids := displayOrder(current)
	listed := map[string]bool{}
	var order []string
	for _, id := range desired {
		if slices.Contains(ids, id) && !listed[id] {
			listed[id] = true
			order = append(order, id)
		}
	}
	for i, id := range ids {
		if listed[id] {
			ids[i], order = order[0], order[1:]
		}
	}
	return ids
}

// checkReorder verifies the invariants Reorder promises for any input.
func checkReorder(t *testing.T, current []Positioned, desired []string) []PositionUpdate {
	t.Helper()
	updates := Reorder(current, desired)
	after := apply(current, updates)
	want := wantOrder(current, desired)
	if got := displayOrder(after); !slices.Equal(got, want) {
		t.Errorf("display order after %v = %v, want %v", updates, got, want)
	}

	was := map[string]int64{}
	for _, p := range current {
		was[p.ID] = p.Position
	}
	final := map[string]int64{}
	for _, p := range after {
		final[p.ID] = p.Position
	}
	moved := map[string]bool{}
	for _, u := range updates {
		if moved[u.ID] {
			t.Errorf("%s is updated twice in %v", u.ID, updates)
		}
		moved[u.ID] = true
		if was[u.ID] == u.Position {
			t.Errorf("update %v does not change the position", u)
		}
	}
	// A moved item sharing a position with any other item would leave the
	// result up to how Discord resolves the collision.
	for i, a := range after {
		for _, b := range after[i+1:] {
			if a.Position == b.Position && (moved[a.ID] || moved[b.ID]) {
				t.Errorf("%s and %s share position %d after %v", a.ID, b.ID, a.Position, updates)
			}
		}
	}
	// Unlisted items move only when bumped by the item displayed below them.
	listed := map[string]bool{}
	for _, id := range desired {
		listed[id] = true
	}
	for i, id := range want {
		if moved[id] && !listed[id] && (i == 0 || final[id] != final[want[i-1]]+1 || was[id] > final[want[i-1]]) {
			t.Errorf("unlisted %s moved from %d to %d without a collision", id, was[id], final[id])
		}
	}

	if again := Reorder(after, desired); len(again) != 0 {
		t.Errorf("reorder is not idempotent: %v", again)
	}
	return updates
}

func TestReorder(t *testing.T) {
	tests := []struct {
		name        string
		current     []Positioned
		desired     []string
		wantUpdates []PositionUpdate
	}{
		{
			name:    "already ordered",
			current: []Positioned{{"1", 1}, {"2", 2}, {"3", 3}},
			desired: []string{"1", "2", "3"},
		},
		{
			name:        "reverse",
			current:     []Positioned{{"1", 1}, {"2", 2}, {"3", 3}},
			desired:     []string{"3", "2", "1"},
			wantUpdates: []PositionUpdate{{"3", 1}, {"1", 3}},
		},
		{
			name:        "ties are broken",
			current:     []Positioned{{"1", 0}, {"2", 0}, {"3", 0}},
			desired:     []string{"3", "1", "2"},
			wantUpdates: []PositionUpdate{{"1", 1}, {"2", 2}},
		},
		{
			name:        "subset keeps unlisted in place",
			current:     []Positioned{{"1", 1}, {"2", 2}, {"3", 3}, {"4", 4}},
			desired:     []string{"3", "1"},
			wantUpdates: []PositionUpdate{{"3", 1}, {"1", 3}},
		},
		{
			name:        "unknown IDs are ignored",
			current:     []Positioned{{"1", 1}, {"2", 2}},
			desired:     []string{"2", "9", "1"},
			wantUpdates: []PositionUpdate{{"2", 1}, {"1", 2}},
		},
		{
			name:    "empty",
			current: nil,
			desired: nil,
		},
		{
			// Bumping "10" to 2 alone would tie it with the unlisted "5",
			// which sorts first by snowflake.
			name:        "bump collides with unlisted",
			current:     []Positioned{{"1", 0}, {"10", 1}, {"11", 1}, {"5", 2}},
			desired:     []string{"11", "10"},
			wantUpdates: []PositionUpdate{{"10", 2}, {"5", 3}},
		},
		{
			name:        "collision cascades through listed and unlisted",
			current:     []Positioned{{"10", 1}, {"11", 1}, {"5", 2}, {"12", 3}, {"6", 4}, {"7", 9}},
			desired:     []string{"11", "10", "12"},
			wantUpdates: []PositionUpdate{{"10", 2}, {"5", 3}, {"12", 4}, {"6", 5}},
		},
		{
			name:        "interleaved ties",
			current:     []Positioned{{"1", 1}, {"2", 1}, {"3", 1}, {"4", 1}},
			desired:     []string{"4", "2"},
			wantUpdates: []PositionUpdate{{"3", 2}, {"2", 3}},
		},
		{
			// "2" lands on the position "10" already holds, and sorts
			// before it, but must not share it.
			name:        "moved item collides with unlisted tie",
			current:     []Positioned{{"9", 1}, {"10", 1}, {"2", 5}},
			desired:     []string{"2", "9"},
			wantUpdates: []PositionUpdate{{"2", 1}, {"10", 2}, {"9", 5}},
		},
		{
			name:        "unmoved ties are kept",
			current:     []Positioned{{"1", 1}, {"2", 1}, {"3", 5}, {"4", 6}},
			desired:     []string{"4", "3"},
			wantUpdates: []PositionUpdate{{"4", 5}, {"3", 6}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updates := checkReorder(t, tt.current, tt.desired)
			if !slices.Equal(updates, tt.wantUpdates) {
				t.Errorf("updates = %v, want %v", updates, tt.wantUpdates)
			}
		})
	}
}

// TestReorderRandom checks the invariants on random inputs with many ties and
// snowflakes of different lengths.
func TestReorderRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // A fixed seed keeps failures reproducible.
	for i := range 5000 {
		n := rng.IntN(10)
		current := make([]Positioned, n)
		for j, id := range rng.Perm(n) {
			current[j] = Positioned{ID: strconv.Itoa(id * 7), Position: rng.Int64N(int64(n) + 1)}
		}
		var desired []string
		for _, j := range rng.Perm(n)[:rng.IntN(n+1)] {
			desired = append(desired, current[j].ID)
		}
		if rng.IntN(4) == 0 {
			desired = append(desired, "999")
		}
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			checkReorder(t, current, desired)
		})
	}
}

func TestSortByPositionTiesBySnowflake(t *testing.T) {
	items := []Positioned{{"300", 1}, {"1000", 1}, {"20", 1}, {"5", 0}}
	if got, want := displayOrder(items), []string{"5", "20", "300", "1000"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}
