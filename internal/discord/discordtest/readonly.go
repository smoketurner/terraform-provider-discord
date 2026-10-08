package discordtest

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// readOnlyState holds data the provider only reads: audit logs, vanity URLs,
// pins and the global catalogs of regions, sticker packs and sounds.
type readOnlyState struct {
	auditLog map[string][]discord.AuditLogEntry
	vanity   map[string]discord.VanityURL
	// pins lists pinned messages in the order they were pinned.
	pins   []pin
	pinSeq int
}

type pin struct {
	channelID string
	messageID string
	pinnedAt  string
}

// pinEpoch is the time of the first pin; each later pin is a second later
// so that pins have distinct, ordered timestamps.
var pinEpoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// VoiceRegions are the regions GET /voice/regions returns.
var VoiceRegions = []discord.VoiceRegion{
	{ID: "us-east", Name: "US East", Optimal: true},
	{ID: "us-west", Name: "US West"},
	{ID: "hongkong", Name: "Hong Kong", Deprecated: true},
}

// VIPVoiceRegion is added to the guild regions of guilds with the
// VIP_REGIONS feature.
var VIPVoiceRegion = discord.VoiceRegion{ID: "vip-us-east", Name: "VIP US East", Custom: true}

// StickerPacks are the standard sticker packs the fake serves.
var StickerPacks = []discord.StickerPack{
	{
		ID:   "300000000000000001",
		Name: "Wumpus Beyond",
		Stickers: []discord.StandardSticker{
			{ID: "300000000000000011", PackID: "300000000000000001", Name: "Wave", Description: new("Wumpus waves"), Tags: "wave", Type: 1, FormatType: 3, SortValue: 1},
			{ID: "300000000000000012", PackID: "300000000000000001", Name: "Dance", Tags: "dance", Type: 1, FormatType: 1, SortValue: 2},
		},
		SKUID:          "300000000000000002",
		CoverStickerID: new("300000000000000011"),
		Description:    "Say hello to Wumpus!",
		BannerAssetID:  new("300000000000000003"),
	},
	{
		ID:          "300000000000000021",
		Name:        "Doggos",
		Stickers:    []discord.StandardSticker{},
		SKUID:       "300000000000000022",
		Description: "Dogs.",
	},
}

// DefaultSoundboardSounds are the sounds GET /soundboard-default-sounds
// returns.
var DefaultSoundboardSounds = []discord.SoundboardSound{
	{SoundID: "1", Name: "quack", Volume: 1, EmojiName: new("🦆"), Available: true},
	{SoundID: "2", Name: "airhorn", Volume: 0.5, EmojiName: new("🔊"), Available: true},
}

func (s *Server) handleReadOnly(mux *http.ServeMux) {
	mux.HandleFunc("GET /voice/regions", s.listVoiceRegions)
	mux.HandleFunc("GET /guilds/{guild}/regions", s.listGuildVoiceRegions)
	mux.HandleFunc("GET /guilds/{guild}/preview", s.getGuildPreview)
	mux.HandleFunc("GET /guilds/{guild}/vanity-url", s.getVanityURL)
	mux.HandleFunc("GET /guilds/{guild}/widget.json", s.getGuildWidget)
	mux.HandleFunc("GET /guilds/{guild}/audit-logs", s.getAuditLog)
	mux.HandleFunc("GET /guilds/{guild}/roles/member-counts", s.getRoleMemberCounts)
	mux.HandleFunc("GET /invites/{code}", s.getInvite)
	mux.HandleFunc("GET /channels/{channel}/messages/pins", s.listPins)
	mux.HandleFunc("GET /stickers/{sticker}", s.getStandardSticker)
	mux.HandleFunc("GET /sticker-packs", s.listStickerPacks)
	mux.HandleFunc("GET /sticker-packs/{pack}", s.getStickerPack)
	mux.HandleFunc("GET /soundboard-default-sounds", s.listDefaultSoundboardSounds)
}

func (s *Server) readOnly() *readOnlyState {
	if s.ro == nil {
		s.ro = &readOnlyState{auditLog: map[string][]discord.AuditLogEntry{}, vanity: map[string]discord.VanityURL{}}
	}
	return s.ro
}

// snowflakeCompare orders snowflakes numerically.
func snowflakeCompare(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// queryLimit parses the limit query parameter, writing 400 when it is
// outside 1-maxLimit.
func queryLimit(w http.ResponseWriter, r *http.Request, def, maxLimit int) (int, bool) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxLimit {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: limit")
		return 0, false
	}
	return n, true
}

func (s *Server) listVoiceRegions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, VoiceRegions)
}

func (s *Server) listGuildVoiceRegions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	regions := slices.Clone(VoiceRegions)
	if slices.Contains(g.Features, "VIP_REGIONS") {
		regions = append(regions, VIPVoiceRegion)
	}
	writeJSON(w, http.StatusOK, regions)
}

func (s *Server) getGuildPreview(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	emojis := []discord.Emoji{}
	for _, e := range s.emojis[g.ID] {
		emojis = append(emojis, *e)
	}
	slices.SortFunc(emojis, func(a, b discord.Emoji) int { return snowflakeCompare(a.ID, b.ID) })
	writeJSON(w, http.StatusOK, discord.GuildPreview{
		ID:                       g.ID,
		Name:                     g.Name,
		Icon:                     g.Icon,
		Emojis:                   emojis,
		Features:                 g.Features,
		ApproximateMemberCount:   int64(len(s.members[g.ID])),
		ApproximatePresenceCount: 1,
		Description:              g.Description,
		Stickers:                 []discord.Sticker{},
	})
}

// SetVanityURL sets a guild's vanity invite code and use count, as an owner
// would in the client.
func (s *Server) SetVanityURL(guildID, code string, uses int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readOnly().vanity[guildID] = discord.VanityURL{Code: &code, Uses: uses}
}

func (s *Server) getVanityURL(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	if !slices.Contains(g.Features, "VANITY_URL") {
		writeError(w, http.StatusForbidden, 50013, "Missing Permissions")
		return
	}
	writeJSON(w, http.StatusOK, s.readOnly().vanity[g.ID])
}

// getGuildWidget lists the voice and stage channels as public, since the
// fake does not model @everyone's permissions on them.
func (s *Server) getGuildWidget(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, gs, ok := s.guildSettings(w, r)
	if !ok {
		return
	}
	if !gs.widget.Enabled {
		writeError(w, http.StatusForbidden, 50004, "Widget Disabled")
		return
	}
	widget := discord.GuildWidget{ID: g.ID, Name: g.Name, Channels: []discord.WidgetChannel{}, Members: []discord.WidgetMember{}}
	for _, ch := range s.channels {
		if ch.GuildID == g.ID && (ch.Type == discord.ChannelTypeVoice || ch.Type == discord.ChannelTypeStage) {
			widget.Channels = append(widget.Channels, discord.WidgetChannel{ID: ch.ID, Name: ch.Name, Position: ch.Position})
		}
	}
	slices.SortFunc(widget.Channels, func(a, b discord.WidgetChannel) int { return snowflakeCompare(a.ID, b.ID) })
	if gs.widget.ChannelID != nil {
		invite := "https://discord.com/invite/widget" + *gs.widget.ChannelID
		widget.InstantInvite = &invite
	}
	widget.Members = append(widget.Members, discord.WidgetMember{
		ID: "0", Username: "tester", Status: "online", AvatarURL: "https://cdn.discordapp.com/widget-avatars/0",
	})
	widget.PresenceCount = int64(len(widget.Members))
	writeJSON(w, http.StatusOK, widget)
}

// AddAuditLogEntry appends an entry to a guild's audit log, assigning its ID
// when empty. IDs increase, so later entries are newer.
func (s *Server) AddAuditLogEntry(guildID string, e discord.AuditLogEntry) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.ID == "" {
		e.ID = s.newID()
	}
	ro := s.readOnly()
	ro.auditLog[guildID] = append(ro.auditLog[guildID], e)
	return e.ID
}

// getAuditLog returns entries newest first, or oldest first when after is
// set, as Discord documents.
func (s *Server) getAuditLog(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	limit, ok := queryLimit(w, r, 50, 100)
	if !ok {
		return
	}
	q := r.URL.Query()
	before, after := q.Get("before"), q.Get("after")
	entries := []discord.AuditLogEntry{}
	for _, e := range s.readOnly().auditLog[g.ID] {
		switch {
		case q.Has("user_id") && (e.UserID == nil || *e.UserID != q.Get("user_id")),
			q.Has("action_type") && strconv.FormatInt(e.ActionType, 10) != q.Get("action_type"),
			before != "" && snowflakeCompare(e.ID, before) >= 0,
			after != "" && snowflakeCompare(e.ID, after) <= 0:
			continue
		}
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b discord.AuditLogEntry) int { return snowflakeCompare(b.ID, a.ID) })
	if after != "" {
		slices.Reverse(entries)
	}
	writeJSON(w, http.StatusOK, discord.AuditLog{AuditLogEntries: entries[:min(limit, len(entries))]})
}

func (s *Server) getRoleMemberCounts(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	counts := map[string]int64{}
	for id := range s.roles[g.ID] {
		if id != g.ID {
			counts[id] = 0
		}
	}
	for _, m := range s.members[g.ID] {
		for _, id := range m.Roles {
			if _, ok := counts[id]; ok {
				counts[id]++
			}
		}
	}
	writeJSON(w, http.StatusOK, counts)
}

// getInvite resolves an invite with its guild and channel. Counts are only
// included with with_counts=true, and the creation metadata is never
// included, as on Discord.
func (s *Server) getInvite(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invites[r.PathValue("code")]
	if !ok {
		notFound(w, "Invite", 10006)
		return
	}
	out := discord.Invite{Code: inv.Code, ExpiresAt: inv.ExpiresAt, Inviter: &discord.User{ID: s.botUserID, Username: "bot", Bot: true}}
	if ch, ok := s.channels[inv.Channel.ID]; ok {
		out.Channel = &discord.InviteChannel{ID: ch.ID, Name: ch.Name, Type: ch.Type}
		if g, ok := s.guilds[ch.GuildID]; ok {
			out.Guild = &discord.InviteGuild{ID: g.ID, Name: g.Name, Description: g.Description, Icon: g.Icon, Features: g.Features}
		}
	}
	if r.URL.Query().Get("with_counts") == "true" && out.Guild != nil {
		members, presences := int64(len(s.members[out.Guild.ID])), int64(1)
		out.ApproximateMemberCount, out.ApproximatePresenceCount = &members, &presences
	}
	writeJSON(w, http.StatusOK, out)
}

// recordPin keeps the pin list in step with a message's pinned flag.
func (s *Server) recordPin(m *discord.Message, pinned bool) {
	ro := s.readOnly()
	i := slices.IndexFunc(ro.pins, func(p pin) bool { return p.messageID == m.ID })
	switch {
	case pinned && i < 0:
		at := pinEpoch.Add(time.Duration(ro.pinSeq) * time.Second).Format("2006-01-02T15:04:05.000000+00:00")
		ro.pinSeq++
		ro.pins = append(ro.pins, pin{channelID: m.ChannelID, messageID: m.ID, pinnedAt: at})
	case !pinned && i >= 0:
		ro.pins = slices.Delete(ro.pins, i, i+1)
	}
}

// listPins returns a channel's pins most recently pinned first, paginated
// by pin time.
func (s *Server) listPins(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channel(w, r)
	if !ok {
		return
	}
	limit, ok := queryLimit(w, r, 50, 50)
	if !ok {
		return
	}
	var before time.Time
	if b := r.URL.Query().Get("before"); b != "" {
		var err error
		if before, err = time.Parse(time.RFC3339Nano, b); err != nil {
			writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: before")
			return
		}
	}
	out := discord.MessagePins{Items: []discord.MessagePin{}}
	for _, p := range slices.Backward(s.readOnly().pins) {
		if p.channelID != ch.ID {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, p.pinnedAt)
		if !before.IsZero() && !at.Before(before) {
			continue
		}
		if len(out.Items) == limit {
			out.HasMore = true
			break
		}
		out.Items = append(out.Items, discord.MessagePin{PinnedAt: p.pinnedAt, Message: *s.messages[p.messageID]})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getStandardSticker(w http.ResponseWriter, r *http.Request) {
	for _, p := range StickerPacks {
		for _, st := range p.Stickers {
			if st.ID == r.PathValue("sticker") {
				writeJSON(w, http.StatusOK, st)
				return
			}
		}
	}
	notFound(w, "Sticker", 10060)
}

func (s *Server) listStickerPacks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, discord.StickerPacks{StickerPacks: StickerPacks})
}

func (s *Server) getStickerPack(w http.ResponseWriter, r *http.Request) {
	for _, p := range StickerPacks {
		if p.ID == r.PathValue("pack") {
			writeJSON(w, http.StatusOK, p)
			return
		}
	}
	notFound(w, "Sticker Pack", 10061)
}

func (s *Server) listDefaultSoundboardSounds(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, DefaultSoundboardSounds)
}
