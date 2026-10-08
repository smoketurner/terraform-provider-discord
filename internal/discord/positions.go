package discord

import (
	"cmp"
	"slices"
)

// Positioned is a role or channel with a sort position.
type Positioned struct {
	ID       string
	Position int64
}

// SortByPosition orders items the way Discord displays them: ascending by
// position, ties broken by ascending snowflake.
func SortByPosition(items []Positioned) {
	slices.SortStableFunc(items, func(a, b Positioned) int {
		if c := cmp.Compare(a.Position, b.Position); c != 0 {
			return c
		}
		return compareSnowflakes(a.ID, b.ID)
	})
}

func compareSnowflakes(a, b string) int {
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	return cmp.Compare(a, b)
}

// Reorder computes the position updates that arrange the desired IDs in the
// given ascending order. The desired items reuse the position slots they
// already occupy, so items not listed keep their place. Slots are made
// strictly increasing to break ties. IDs missing from current are ignored.
func Reorder(current []Positioned, desired []string) []PositionUpdate {
	byID := make(map[string]Positioned, len(current))
	for _, p := range current {
		byID[p.ID] = p
	}
	var occupied []Positioned
	var order []string
	for _, id := range desired {
		if p, ok := byID[id]; ok {
			occupied = append(occupied, p)
			order = append(order, id)
		}
	}
	SortByPosition(occupied)

	var updates []PositionUpdate
	var prev int64
	for i, id := range order {
		slot := occupied[i].Position
		if i > 0 && slot <= prev {
			slot = prev + 1
		}
		prev = slot
		if byID[id].Position != slot {
			updates = append(updates, PositionUpdate{ID: id, Position: slot})
		}
	}
	return updates
}

// OrderOf returns the IDs from want that exist in current, in display order.
func OrderOf(current []Positioned, want []string) []string {
	wanted := make(map[string]bool, len(want))
	for _, id := range want {
		wanted[id] = true
	}
	var present []Positioned
	for _, p := range current {
		if wanted[p.ID] {
			present = append(present, p)
		}
	}
	SortByPosition(present)
	ids := make([]string, len(present))
	for i, p := range present {
		ids[i] = p.ID
	}
	return ids
}
