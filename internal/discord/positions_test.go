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

func displayOrder(o Ordering, items []Positioned) []string {
	sorted := slices.Clone(items)
	o.Sort(sorted)
	ids := make([]string, len(sorted))
	for i, p := range sorted {
		ids[i] = p.ID
	}
	return ids
}

// wantOrder is the display order after reordering: the current order with the
// slots of the listed items filled in the desired order.
func wantOrder(o Ordering, current []Positioned, desired []string) []string {
	ids := displayOrder(o, current)
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
func checkReorder(t *testing.T, o Ordering, current []Positioned, desired []string) []PositionUpdate {
	t.Helper()
	updates := o.Reorder(current, desired)
	after := apply(current, updates)
	want := wantOrder(o, current, desired)
	if got := displayOrder(o, after); !slices.Equal(got, want) {
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
	// Unlisted items move only when the item displayed below them would
	// otherwise be above them, and then only to the lowest position that
	// keeps them above it.
	listed := map[string]bool{}
	for _, id := range desired {
		listed[id] = true
	}
	for i, id := range want {
		if !moved[id] || listed[id] {
			continue
		}
		if i == 0 {
			t.Errorf("unlisted %s moved from %d to %d without an item below it", id, was[id], final[id])
			continue
		}
		below := want[i-1]
		lowest := final[below] + 1
		if o.compareTied(below, id) < 0 {
			lowest = final[below]
		}
		if was[id] >= lowest || final[id] != lowest {
			t.Errorf("unlisted %s moved from %d to %d; the item below it is at %d", id, was[id], final[id], final[below])
		}
	}

	if again := o.Reorder(after, desired); len(again) != 0 {
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
			// "1" and "2" can share a position, as tied items sort by ID.
			name:        "ties are broken",
			current:     []Positioned{{"1", 0}, {"2", 0}, {"3", 0}},
			desired:     []string{"3", "1", "2"},
			wantUpdates: []PositionUpdate{{"1", 1}, {"2", 1}},
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
			wantUpdates: []PositionUpdate{{"10", 2}, {"5", 3}},
		},
		{
			name:        "interleaved ties",
			current:     []Positioned{{"1", 1}, {"2", 1}, {"3", 1}, {"4", 1}},
			desired:     []string{"4", "2"},
			wantUpdates: []PositionUpdate{{"3", 2}, {"2", 3}},
		},
		{
			// "2" lands on the position "10" already holds and sorts
			// before it by ID, so they share it.
			name:        "moved item ties with unlisted",
			current:     []Positioned{{"9", 1}, {"10", 1}, {"2", 5}},
			desired:     []string{"2", "9"},
			wantUpdates: []PositionUpdate{{"2", 1}, {"9", 5}},
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
			updates := checkReorder(t, ChannelOrder, tt.current, tt.desired)
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
			checkReorder(t, ChannelOrder, current, desired)
			checkReorder(t, RoleOrder, current, desired)
		})
	}
}

func TestSortTies(t *testing.T) {
	items := []Positioned{{"300", 1}, {"1000", 1}, {"20", 1}, {"5", 0}}
	if got, want := displayOrder(ChannelOrder, items), []string{"5", "20", "300", "1000"}; !slices.Equal(got, want) {
		t.Errorf("channel order = %v, want %v", got, want)
	}
	if got, want := displayOrder(RoleOrder, items), []string{"5", "1000", "300", "20"}; !slices.Equal(got, want) {
		t.Errorf("role order = %v, want %v", got, want)
	}
	if !RoleOrder.Below(Positioned{"1000", 1}, Positioned{"20", 1}) || RoleOrder.Below(Positioned{"20", 1}, Positioned{"1000", 1}) {
		t.Error("a newer role tied with an older one is not below it")
	}
}

// The live server: new roles 10, 11 and 12 share position 1 with the older
// unlisted role 4, below roles 3 and 2 and the bot's role 1 at position 4.
// Separating the new roles moves only roles below the bot's.
func TestReorderRolesBelowBot(t *testing.T) {
	current := []Positioned{{"0", 0}, {"4", 1}, {"10", 1}, {"11", 1}, {"12", 1}, {"3", 2}, {"2", 3}, {"1", 4}}
	// From the top: 12, 10, 11 (ascending: 11, 10, 12).
	updates := checkReorder(t, RoleOrder, current, []string{"11", "10", "12"})
	if want := []PositionUpdate{{"12", 2}, {"4", 2}}; !slices.Equal(updates, want) {
		t.Errorf("updates = %v, want %v", updates, want)
	}
}

// Roles created at the bottom share position 1; the older role is higher.
func TestReorderRoleTies(t *testing.T) {
	current := []Positioned{{"1", 0}, {"10", 1}, {"11", 1}, {"12", 1}}
	if updates := RoleOrder.Reorder(current, []string{"12", "11", "10"}); len(updates) != 0 {
		t.Errorf("roles already in order moved: %v", updates)
	}
	updates := checkReorder(t, RoleOrder, current, []string{"10", "11", "12"})
	if want := []PositionUpdate{{"11", 2}, {"12", 3}}; !slices.Equal(updates, want) {
		t.Errorf("updates = %v, want %v", updates, want)
	}
}
