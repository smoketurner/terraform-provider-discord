package discordtest

import (
	"cmp"
	"maps"
	"net/http"
	"slices"
	"strconv"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// listState holds guild objects the provider only lists. Tests seed them
// with the Add* helpers.
type listState struct {
	integrations map[string][]discord.Integration
}

func (s *Server) handleLists(mux *http.ServeMux) {
	mux.HandleFunc("GET /guilds/{guild}/members", s.listMembers)
	mux.HandleFunc("GET /guilds/{guild}/bans", s.listBans)
	mux.HandleFunc("GET /guilds/{guild}/emojis", s.listEmojis)
	mux.HandleFunc("GET /guilds/{guild}/stickers", s.listStickers)
	mux.HandleFunc("GET /guilds/{guild}/webhooks", s.listGuildWebhooks)
	mux.HandleFunc("GET /guilds/{guild}/invites", s.listGuildInvites)
	mux.HandleFunc("GET /guilds/{guild}/scheduled-events", s.listScheduledEvents)
	mux.HandleFunc("GET /guilds/{guild}/threads/active", s.listActiveThreads)
	mux.HandleFunc("GET /guilds/{guild}/auto-moderation/rules", s.listAutomodRules)
	mux.HandleFunc("GET /guilds/{guild}/integrations", s.listIntegrations)
	mux.HandleFunc("GET /channels/{channel}/webhooks", s.listChannelWebhooks)
	mux.HandleFunc("GET /channels/{channel}/threads/archived/public", s.listArchivedThreads(false))
	mux.HandleFunc("GET /channels/{channel}/threads/archived/private", s.listArchivedThreads(true))
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

func (s *Server) listStickers(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sortedByID(s.stickers[g.ID], func(*discord.Sticker) bool { return true }))
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

func (s *Server) listAutomodRules(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	out := []map[string]any{}
	for _, rule := range sortedByID(s.automod[g.ID], func(*discord.AutoModerationRule) bool { return true }) {
		out = append(out, automodJSON(rule))
	}
	writeJSON(w, http.StatusOK, out)
}

// AddIntegration adds an integration to a guild and returns its ID.
func (s *Server) AddIntegration(guildID, name, typ string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lists.integrations == nil {
		s.lists.integrations = map[string][]discord.Integration{}
	}
	id := s.newID()
	s.lists.integrations[guildID] = append(s.lists.integrations[guildID], discord.Integration{
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
	writeJSON(w, http.StatusOK, append([]discord.Integration{}, s.lists.integrations[g.ID]...))
}

func (s *Server) listChannelWebhooks(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channel(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sortedByID(s.webhooks, func(wh *discord.Webhook) bool { return wh.ChannelID == ch.ID }))
}

// listArchivedThreads models List Public and Private Archived Threads: the
// channel's archived threads of the requested visibility, most recently
// archived first, before the "before" timestamp, in pages of "limit" (2-100).
func (s *Server) listArchivedThreads(private bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		ch, ok := s.channel(w, r)
		if !ok {
			return
		}
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 2 || n > discord.ArchivedThreadsPageSize {
				invalidForm(w, "limit must be between 2 and 100")
				return
			}
			limit = n
		}
		before := r.URL.Query().Get("before")
		threads := sortedByID(s.threads, func(t *discord.Thread) bool {
			meta := t.ThreadMetadata
			return *t.ParentID == ch.ID && meta.Archived && meta.ArchiveTimestamp != nil &&
				(t.Type == discord.ChannelTypePrivateThread) == private &&
				(before == "" || *meta.ArchiveTimestamp < before)
		})
		slices.SortStableFunc(threads, func(a, b *discord.Thread) int {
			return cmp.Compare(*b.ThreadMetadata.ArchiveTimestamp, *a.ThreadMetadata.ArchiveTimestamp)
		})
		hasMore := len(threads) > limit
		writeJSON(w, http.StatusOK, map[string]any{"threads": threads[:min(limit, len(threads))], "members": []any{}, "has_more": hasMore})
	}
}
