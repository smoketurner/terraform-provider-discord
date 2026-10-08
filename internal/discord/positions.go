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
// given ascending order. The desired items reuse the display slots they
// already occupy, so items not listed keep their place relative to every
// other item. IDs missing from current are ignored.
//
// Tied slots are bumped to make the listed order strict. A bumped item must
// not share a position with an item that is not moved, because Discord then
// shifts the unmoved item, so the following items are bumped along with it,
// listed or not, until the positions no longer collide.
func Reorder(current []Positioned, desired []string) []PositionUpdate {
	was := make(map[string]int64, len(current))
	for _, p := range current {
		was[p.ID] = p.Position
	}
	listed := make(map[string]bool, len(desired))
	var order []string
	for _, id := range desired {
		if _, ok := was[id]; ok && !listed[id] {
			listed[id] = true
			order = append(order, id)
		}
	}
	slots := slices.Clone(current)
	SortByPosition(slots)

	var updates []PositionUpdate
	var prev Positioned
	var prevMoved bool
	for i, slot := range slots {
		item := slot
		if listed[slot.ID] {
			item.ID, order = order[0], order[1:]
		}
		// Equal positions are only kept between unmoved items already
		// displayed in this order.
		tieKept := item.Position == prev.Position && !prevMoved &&
			was[item.ID] == item.Position && compareSnowflakes(prev.ID, item.ID) < 0
		if i > 0 && item.Position <= prev.Position && !tieKept {
			item.Position = prev.Position + 1
		}
		moved := was[item.ID] != item.Position
		if moved {
			updates = append(updates, PositionUpdate(item))
		}
		prev, prevMoved = item, moved
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
