package discordtest

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// monetization holds the application's SKUs, entitlements and
// subscriptions.
type monetization struct {
	skus          []discord.SKU
	entitlements  []discord.Entitlement
	subscriptions []discord.Subscription
}

func (s *Server) handleMonetization(mux *http.ServeMux) {
	mux.HandleFunc("GET /applications/{app}/skus", s.listSKUs)
	mux.HandleFunc("GET /applications/{app}/entitlements", s.listEntitlements)
	mux.HandleFunc("GET /skus/{sku}/subscriptions", s.listSKUSubscriptions)
}

func (s *Server) monetization() *monetization {
	if s.money == nil {
		s.money = &monetization{}
	}
	return s.money
}

// AddSKU adds an SKU to the application, assigning its ID.
func (s *Server) AddSKU(sku discord.SKU) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sku.ID = s.newID()
	sku.ApplicationID = ApplicationID
	m := s.monetization()
	m.skus = append(m.skus, sku)
	return sku.ID
}

// AddEntitlement adds an entitlement of the application, assigning its ID.
// IDs increase, so later entitlements are newer.
func (s *Server) AddEntitlement(e discord.Entitlement) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.ID = s.newID()
	e.ApplicationID = ApplicationID
	m := s.monetization()
	m.entitlements = append(m.entitlements, e)
	return e.ID
}

// AddSubscription adds a subscription, assigning its ID.
func (s *Server) AddSubscription(sub discord.Subscription) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub.ID = s.newID()
	m := s.monetization()
	m.subscriptions = append(m.subscriptions, sub)
	return sub.ID
}

func (s *Server) knownApplication(w http.ResponseWriter, r *http.Request) bool {
	if r.PathValue("app") != ApplicationID {
		notFound(w, "Application", 10002)
		return false
	}
	return true
}

func (s *Server) listSKUs(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.knownApplication(w, r) {
		writeJSON(w, http.StatusOK, append([]discord.SKU{}, s.monetization().skus...))
	}
}

// page applies before, after and limit to items: like messages, before
// selects the newest items older than it and after the oldest newer than
// it. The page is in ascending ID order either way, so callers must not
// rely on the order to paginate.
func page[T any](w http.ResponseWriter, r *http.Request, items []T, id func(T) string, def int) ([]T, bool) {
	limit, ok := queryLimit(w, r, def, 100)
	if !ok {
		return nil, false
	}
	q := r.URL.Query()
	before, after := q.Get("before"), q.Get("after")
	var out []T
	for _, it := range items {
		if (before == "" || snowflakeCompare(id(it), before) < 0) && (after == "" || snowflakeCompare(id(it), after) > 0) {
			out = append(out, it)
		}
	}
	slices.SortFunc(out, func(a, b T) int { return snowflakeCompare(id(a), id(b)) })
	if len(out) > limit {
		if after != "" {
			out = out[:limit]
		} else {
			out = out[len(out)-limit:]
		}
	}
	if out == nil {
		out = []T{}
	}
	return out, true
}

func (s *Server) listEntitlements(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.knownApplication(w, r) {
		return
	}
	q := r.URL.Query()
	var skuIDs []string
	if v := q.Get("sku_ids"); v != "" {
		skuIDs = strings.Split(v, ",")
	}
	excludeDeleted := q.Get("exclude_deleted") != "false"
	now := time.Now().UTC().Format(time.RFC3339)
	var matches []discord.Entitlement
	for _, e := range s.monetization().entitlements {
		switch {
		case q.Has("user_id") && (e.UserID == nil || *e.UserID != q.Get("user_id")),
			q.Has("guild_id") && (e.GuildID == nil || *e.GuildID != q.Get("guild_id")),
			skuIDs != nil && !slices.Contains(skuIDs, e.SKUID),
			excludeDeleted && e.Deleted,
			q.Get("exclude_ended") == "true" && e.EndsAt != nil && *e.EndsAt < now:
			continue
		}
		matches = append(matches, e)
	}
	if out, ok := page(w, r, matches, func(e discord.Entitlement) string { return e.ID }, 100); ok {
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *Server) listSKUSubscriptions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user := r.URL.Query().Get("user_id")
	if user == "" {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: user_id is required")
		return
	}
	var matches []discord.Subscription
	for _, sub := range s.monetization().subscriptions {
		if sub.UserID == user && slices.Contains(sub.SKUIDs, r.PathValue("sku")) {
			matches = append(matches, sub)
		}
	}
	if out, ok := page(w, r, matches, func(sub discord.Subscription) string { return sub.ID }, 50); ok {
		writeJSON(w, http.StatusOK, out)
	}
}
