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

// Ordering is how Discord orders items that share a position, which differs
// between channels and roles.
type Ordering struct {
	// olderLast puts the older (lower snowflake) of two tied items later in
	// ascending order.
	olderLast bool
}

var (
	// ChannelOrder sorts tied channels by ascending ID: the older channel is
	// displayed above.
	ChannelOrder = Ordering{}
	// RoleOrder sorts tied roles so the older role is higher, as Discord's
	// role hierarchy and client do. In ascending position order the older
	// role therefore comes last.
	RoleOrder = Ordering{olderLast: true}
)

// compareTied orders two items that share a position in ascending order.
func (o Ordering) compareTied(a, b string) int {
	if o.olderLast {
		return compareSnowflakes(b, a)
	}
	return compareSnowflakes(a, b)
}

// Below reports whether a comes before b in ascending position order.
func (o Ordering) Below(a, b Positioned) bool {
	return a.Position < b.Position || a.Position == b.Position && o.compareTied(a.ID, b.ID) < 0
}

// Sort orders items ascending by position, ties broken as Discord does.
func (o Ordering) Sort(items []Positioned) {
	slices.SortStableFunc(items, func(a, b Positioned) int {
		if c := cmp.Compare(a.Position, b.Position); c != 0 {
			return c
		}
		return o.compareTied(a.ID, b.ID)
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
// Items may share a position when Discord sorts them in the desired order,
// as it sorts items with the same position by ID. Otherwise an item placed
// at or below the previous item's position is raised just enough to follow
// it, and the items after it are raised along with it, listed or not, only
// as far as needed to keep their place. Raising items as little as possible
// keeps the changes below a bot's highest role where possible.
func (o Ordering) Reorder(current []Positioned, desired []string) []PositionUpdate {
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
	o.Sort(slots)

	var updates []PositionUpdate
	var prev Positioned
	for i, slot := range slots {
		item := slot
		if listed[slot.ID] {
			item.ID, order = order[0], order[1:]
		}
		if i > 0 {
			lowest := prev.Position + 1
			if o.compareTied(prev.ID, item.ID) < 0 {
				lowest = prev.Position
			}
			item.Position = max(item.Position, lowest)
		}
		if was[item.ID] != item.Position {
			updates = append(updates, PositionUpdate(item))
		}
		prev = item
	}
	return updates
}

// OrderOf returns the IDs from want that exist in current, in ascending
// position order.
func (o Ordering) OrderOf(current []Positioned, want []string) []string {
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
	o.Sort(present)
	ids := make([]string, len(present))
	for i, p := range present {
		ids[i] = p.ID
	}
	return ids
}
