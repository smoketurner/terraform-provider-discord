package discordtest

import (
	"cmp"
	"maps"
	"net/http"
	"slices"
	"strconv"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// readOnlyState holds guild objects the provider only lists. Tests seed them
// with the Add* helpers.
type readOnlyState struct {
	integrations map[string][]discord.Integration
	templates    map[string][]discord.GuildTemplate
}

func (s *Server) handleLists(mux *http.ServeMux) {
	mux.HandleFunc("GET /guilds/{guild}/members", s.listMembers)
	mux.HandleFunc("GET /guilds/{guild}/bans", s.listBans)
	mux.HandleFunc("GET /guilds/{guild}/emojis", s.listEmojis)
	mux.HandleFunc("GET /guilds/{guild}/webhooks", s.listGuildWebhooks)
	mux.HandleFunc("GET /guilds/{guild}/invites", s.listGuildInvites)
	mux.HandleFunc("GET /guilds/{guild}/scheduled-events", s.listScheduledEvents)
	mux.HandleFunc("GET /guilds/{guild}/threads/active", s.listActiveThreads)
	mux.HandleFunc("GET /guilds/{guild}/integrations", s.listIntegrations)
	mux.HandleFunc("GET /guilds/{guild}/templates", s.listTemplates)
}

// compareIDs orders snowflakes numerically.
func compareIDs(a, b string) int {
	x, _ := strconv.ParseUint(a, 10, 64)
	y, _ := strconv.ParseUint(b, 10, 64)
	return cmp.Compare(x, y)
}

// userPage returns the items after the "after" user ID, in ascending order of
// user ID, limited to the "limit" query parameter (1-1000, default def).
func userPage[T any](w http.ResponseWriter, r *http.Request, items []T, userID func(T) string, def int) ([]T, bool) {
	limit := def
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > discord.MaxPageSize {
			writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: limit must be between 1 and 1000")
			return nil, false
		}
		limit = n
	}
	after := cmp.Or(r.URL.Query().Get("after"), "0")
	slices.SortFunc(items, func(a, b T) int { return compareIDs(userID(a), userID(b)) })
	out := []T{}
	for _, item := range items {
		if compareIDs(userID(item), after) > 0 && len(out) < limit {
			out = append(out, item)
		}
	}
	return out, true
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	var members []*discord.Member
	for _, m := range s.members[g.ID] {
		members = append(members, m)
	}
	if page, ok := userPage(w, r, members, func(m *discord.Member) string { return m.User.ID }, 1); ok {
		writeJSON(w, http.StatusOK, page)
	}
}

func (s *Server) listBans(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	var bans []*discord.Ban
	for _, b := range s.bans[g.ID] {
		bans = append(bans, b)
	}
	if page, ok := userPage(w, r, bans, func(b *discord.Ban) string { return b.User.ID }, discord.MaxPageSize); ok {
		writeJSON(w, http.StatusOK, page)
	}
}

// AddBan bans a user without removing them from the guild's member list.
func (s *Server) AddBan(guildID, userID, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bans[guildID][userID] = &discord.Ban{
		Reason: &reason,
		User:   &discord.User{ID: userID, Username: "user" + userID, Discriminator: "0"},
	}
}

// sortedByID returns the values of m whose key passes keep, ordered by ID.
func sortedByID[T any](m map[string]*T, keep func(*T) bool) []*T {
	out := []*T{}
	for _, id := range slices.SortedFunc(maps.Keys(m), compareIDs) {
		if keep(m[id]) {
			out = append(out, m[id])
		}
	}
	return out
}

func (s *Server) listEmojis(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sortedByID(s.emojis[g.ID], func(*discord.Emoji) bool { return true }))
}

func (s *Server) listGuildWebhooks(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sortedByID(s.webhooks, func(wh *discord.Webhook) bool { return wh.GuildID == g.ID }))
}

func (s *Server) listGuildInvites(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	out := []*discord.Invite{}
	for _, code := range slices.Sorted(maps.Keys(s.invites)) {
		inv := s.invites[code]
		if ch, ok := s.channels[inv.Channel.ID]; ok && ch.GuildID == g.ID {
			out = append(out, inv)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listScheduledEvents(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sortedByID(s.events, func(e *discord.ScheduledEvent) bool { return e.GuildID == g.ID }))
}

// listActiveThreads returns the guild's unarchived threads, newest first as
// Discord orders them.
func (s *Server) listActiveThreads(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	threads := sortedByID(s.threads, func(t *discord.Thread) bool {
		return t.GuildID == g.ID && (t.ThreadMetadata == nil || !t.ThreadMetadata.Archived)
	})
	slices.Reverse(threads)
	writeJSON(w, http.StatusOK, map[string]any{"threads": threads, "members": []any{}})
}

// AddIntegration adds an integration to a guild and returns its ID.
func (s *Server) AddIntegration(guildID, name, typ string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly.integrations == nil {
		s.readOnly.integrations = map[string][]discord.Integration{}
	}
	id := s.newID()
	s.readOnly.integrations[guildID] = append(s.readOnly.integrations[guildID], discord.Integration{
		ID: id, Name: name, Type: typ, Enabled: true,
		Account: discord.IntegrationAccount{ID: s.newID(), Name: name + " account"},
	})
	return id
}

func (s *Server) listIntegrations(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, append([]discord.Integration{}, s.readOnly.integrations[g.ID]...))
}

// AddTemplate adds a template of a guild and returns its code.
func (s *Server) AddTemplate(guildID, name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly.templates == nil {
		s.readOnly.templates = map[string][]discord.GuildTemplate{}
	}
	code := "tpl" + s.newID()
	s.readOnly.templates[guildID] = append(s.readOnly.templates[guildID], discord.GuildTemplate{
		Code: code, Name: name, CreatorID: UserID, SourceGuildID: guildID,
		CreatedAt: "2024-01-01T00:00:00+00:00", UpdatedAt: "2024-01-02T00:00:00+00:00",
	})
	return code
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, append([]discord.GuildTemplate{}, s.readOnly.templates[g.ID]...))
}
