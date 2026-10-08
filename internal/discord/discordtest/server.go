// Package discordtest provides an in-memory fake of the Discord REST API for
// tests. It implements only the endpoints and behavior the provider relies on.
package discordtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// Token is the bot token the fake server accepts.
const Token = "test-token"

// GuildID is the ID of the guild the fake server is seeded with.
const GuildID = "100000000000000001"

// UserID is the ID of a member seeded in the guild.
const UserID = "100000000000000002"

// Server is a fake Discord API backed by in-memory state.
type Server struct {
	*httptest.Server

	mu        sync.Mutex
	nextID    uint64
	guilds    map[string]*discord.Guild
	roles     map[string]map[string]*discord.Role
	channels  map[string]*discord.Channel
	members   map[string]map[string]*discord.Member
	webhooks  map[string]*discord.Webhook
	invites   map[string]*discord.Invite
	messages  map[string]*discord.Message
	emojis    map[string]map[string]*discord.Emoji
	requests  []string
	headers   []http.Header
	edits     []map[string]json.RawMessage
	failNext  map[string]int
	botUserID string
	// hidden channels are omitted from the guild channel list, as Discord
	// does for channels the bot lacks VIEW_CHANNEL on. denied channels are
	// also refused by GET /channels/{id}.
	hidden map[string]bool
	denied map[string]bool
}

// NewServer starts a fake Discord API seeded with one guild containing an
// @everyone role and one member. Call Close when done.
func NewServer() *Server {
	s := &Server{
		nextID:    200000000000000000,
		guilds:    map[string]*discord.Guild{},
		roles:     map[string]map[string]*discord.Role{},
		channels:  map[string]*discord.Channel{},
		members:   map[string]map[string]*discord.Member{},
		webhooks:  map[string]*discord.Webhook{},
		invites:   map[string]*discord.Invite{},
		messages:  map[string]*discord.Message{},
		emojis:    map[string]map[string]*discord.Emoji{},
		failNext:  map[string]int{},
		botUserID: "100000000000000003",
		hidden:    map[string]bool{},
		denied:    map[string]bool{},
	}
	s.guilds[GuildID] = &discord.Guild{
		ID:                GuildID,
		Name:              "Test Server",
		OwnerID:           UserID,
		AFKTimeout:        300,
		PreferredLocale:   "en-US",
		Features:          []string{"COMMUNITY", "NEWS"},
		VerificationLevel: 1,
	}
	s.roles[GuildID] = map[string]*discord.Role{
		GuildID: {ID: GuildID, Name: "@everyone", Permissions: "1071698660929", Colors: &discord.RoleColors{}},
	}
	s.members[GuildID] = map[string]*discord.Member{
		UserID: {User: &discord.User{ID: UserID, Username: "tester", Discriminator: "0"}, Roles: []string{}, JoinedAt: "2024-01-01T00:00:00.000000+00:00"},
	}
	s.emojis[GuildID] = map[string]*discord.Emoji{}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /guilds/{guild}", s.getGuild)
	mux.HandleFunc("PATCH /guilds/{guild}", s.modifyGuild)
	mux.HandleFunc("GET /guilds/{guild}/roles", s.listRoles)
	mux.HandleFunc("POST /guilds/{guild}/roles", s.createRole)
	mux.HandleFunc("PATCH /guilds/{guild}/roles", s.modifyRolePositions)
	mux.HandleFunc("GET /guilds/{guild}/roles/{role}", s.getRole)
	mux.HandleFunc("PATCH /guilds/{guild}/roles/{role}", s.modifyRole)
	mux.HandleFunc("DELETE /guilds/{guild}/roles/{role}", s.deleteRole)
	mux.HandleFunc("GET /guilds/{guild}/channels", s.listChannels)
	mux.HandleFunc("POST /guilds/{guild}/channels", s.createChannel)
	mux.HandleFunc("PATCH /guilds/{guild}/channels", s.modifyChannelPositions)
	mux.HandleFunc("GET /channels/{channel}", s.getChannel)
	mux.HandleFunc("PATCH /channels/{channel}", s.modifyChannel)
	mux.HandleFunc("DELETE /channels/{channel}", s.deleteChannel)
	mux.HandleFunc("PUT /channels/{channel}/permissions/{overwrite}", s.editPermission)
	mux.HandleFunc("DELETE /channels/{channel}/permissions/{overwrite}", s.deletePermission)
	mux.HandleFunc("GET /guilds/{guild}/members/search", s.searchMembers)
	mux.HandleFunc("GET /guilds/{guild}/members/{user}", s.getMember)
	mux.HandleFunc("PUT /guilds/{guild}/members/{user}/roles/{role}", s.addMemberRole)
	mux.HandleFunc("DELETE /guilds/{guild}/members/{user}/roles/{role}", s.removeMemberRole)
	mux.HandleFunc("POST /channels/{channel}/webhooks", s.createWebhook)
	mux.HandleFunc("GET /webhooks/{webhook}", s.getWebhook)
	mux.HandleFunc("PATCH /webhooks/{webhook}", s.modifyWebhook)
	mux.HandleFunc("DELETE /webhooks/{webhook}", s.deleteWebhook)
	mux.HandleFunc("POST /channels/{channel}/invites", s.createInvite)
	mux.HandleFunc("GET /channels/{channel}/invites", s.listChannelInvites)
	mux.HandleFunc("DELETE /invites/{code}", s.deleteInvite)
	mux.HandleFunc("POST /channels/{channel}/messages", s.createMessage)
	mux.HandleFunc("GET /channels/{channel}/messages/{message}", s.getMessage)
	mux.HandleFunc("PATCH /channels/{channel}/messages/{message}", s.editMessage)
	mux.HandleFunc("DELETE /channels/{channel}/messages/{message}", s.deleteMessage)
	mux.HandleFunc("PUT /channels/{channel}/messages/pins/{message}", s.pinMessage(true))
	mux.HandleFunc("DELETE /channels/{channel}/messages/pins/{message}", s.pinMessage(false))
	mux.HandleFunc("GET /guilds/{guild}/emojis/{emoji}", s.getEmoji)
	mux.HandleFunc("POST /guilds/{guild}/emojis", s.createEmoji)
	mux.HandleFunc("PATCH /guilds/{guild}/emojis/{emoji}", s.modifyEmoji)
	mux.HandleFunc("DELETE /guilds/{guild}/emojis/{emoji}", s.deleteEmoji)

	s.Server = httptest.NewServer(s.middleware(mux))
	return s
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bot "+Token {
			writeError(w, http.StatusUnauthorized, 0, "401: Unauthorized")
			return
		}
		key := r.Method + " " + r.URL.Path
		s.mu.Lock()
		s.requests = append(s.requests, key)
		s.headers = append(s.headers, r.Header.Clone())
		fail := s.failNext[key]
		if fail > 0 {
			s.failNext[key] = fail - 1
		}
		s.mu.Unlock()
		if fail > 0 {
			w.Header().Set("X-RateLimit-Global", "false")
			w.Header().Set("Retry-After", "0")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"You are being rate limited.","retry_after":0.01,"global":false}`))
			return
		}
		w.Header().Set("X-RateLimit-Limit", "5")
		w.Header().Set("X-RateLimit-Remaining", "4")
		w.Header().Set("X-RateLimit-Reset-After", "0.001")
		next.ServeHTTP(w, r)
	})
}

// RateLimitNext makes the next n requests matching "METHOD /path" return 429.
func (s *Server) RateLimitNext(methodPath string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext[methodPath] = n
}

// Requests returns the "METHOD /path" of every request received so far.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// RequestHeaders returns the headers of every request matching
// "METHOD /path" received so far, in order.
func (s *Server) RequestHeaders(methodPath string) []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []http.Header
	for i, key := range s.requests {
		if key == methodPath {
			out = append(out, s.headers[i].Clone())
		}
	}
	return out
}

func (s *Server) newID() string {
	s.nextID++
	return strconv.FormatUint(s.nextID, 10)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status, code int, msg string) {
	writeJSON(w, status, map[string]any{"code": code, "message": msg})
}

func notFound(w http.ResponseWriter, what string, code int) {
	writeError(w, http.StatusNotFound, code, "Unknown "+what)
}

// decode reads a JSON or multipart/form-data request body into a map of
// top-level fields.
func decode(r *http.Request) (map[string]json.RawMessage, error) {
	body := map[string]json.RawMessage{}
	if r.ContentLength == 0 {
		return body, nil
	}
	if mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mediaType == "multipart/form-data" {
		return decodeMultipart(r)
	}
	err := json.NewDecoder(r.Body).Decode(&body)
	return body, err
}

// Upload is a file part of a multipart request. decode stores it under its
// form field name ("files[0]", "file") as JSON.
type Upload struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Data        []byte `json:"data"`
}

// decodeMultipart accepts parameters both in payload_json and as plain form
// fields, as Discord does.
func decodeMultipart(r *http.Request) (map[string]json.RawMessage, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, discord.MaxRequestSize)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}
	body := map[string]json.RawMessage{}
	var payloadJSON []byte
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, err
		}
		switch {
		case part.FileName() != "":
			body[part.FormName()], _ = json.Marshal(Upload{Filename: part.FileName(), ContentType: part.Header.Get("Content-Type"), Data: data})
		case part.FormName() == "payload_json":
			payloadJSON = data
		default:
			body[part.FormName()], _ = json.Marshal(string(data))
		}
	}
	if payloadJSON != nil {
		if err := json.Unmarshal(payloadJSON, &body); err != nil {
			return nil, fmt.Errorf("payload_json: %w", err)
		}
	}
	return body, nil
}

// set decodes body[key] into dst when the key is present. A JSON null leaves
// pointer destinations nil.
func set[T any](body map[string]json.RawMessage, key string, dst *T) {
	if raw, ok := body[key]; ok {
		var v T
		_ = json.Unmarshal(raw, &v)
		*dst = v
	}
}

func (s *Server) guild(w http.ResponseWriter, r *http.Request) (*discord.Guild, bool) {
	g, ok := s.guilds[r.PathValue("guild")]
	if !ok {
		notFound(w, "Guild", 10004)
	}
	return g, ok
}

func (s *Server) getGuild(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.guild(w, r); ok {
		writeJSON(w, http.StatusOK, g)
	}
}

func (s *Server) modifyGuild(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	if _, ok := body["icon"]; ok {
		var icon *string
		set(body, "icon", &icon)
		if icon != nil {
			h := fmt.Sprintf("icon%d", len(*icon))
			icon = &h
		}
		g.Icon = icon
	}
	set(body, "name", &g.Name)
	set(body, "description", &g.Description)
	set(body, "afk_channel_id", &g.AFKChannelID)
	set(body, "afk_timeout", &g.AFKTimeout)
	set(body, "verification_level", &g.VerificationLevel)
	set(body, "default_message_notifications", &g.DefaultMessageNotifications)
	set(body, "explicit_content_filter", &g.ExplicitContentFilter)
	set(body, "system_channel_id", &g.SystemChannelID)
	set(body, "system_channel_flags", &g.SystemChannelFlags)
	set(body, "rules_channel_id", &g.RulesChannelID)
	set(body, "public_updates_channel_id", &g.PublicUpdatesChannelID)
	set(body, "safety_alerts_channel_id", &g.SafetyAlertsChannelID)
	set(body, "preferred_locale", &g.PreferredLocale)
	set(body, "premium_progress_bar_enabled", &g.PremiumProgressBarEnabled)
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) sortedRoles(guildID string) []*discord.Role {
	roles := slices.Collect(maps.Values(s.roles[guildID]))
	slices.SortFunc(roles, func(a, b *discord.Role) int {
		if a.Position != b.Position {
			return int(a.Position - b.Position)
		}
		return strings.Compare(a.ID, b.ID)
	})
	return roles
}

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.guild(w, r); ok {
		writeJSON(w, http.StatusOK, s.sortedRoles(r.PathValue("guild")))
	}
}

func (s *Server) getRole(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	role, ok := s.roles[r.PathValue("guild")][r.PathValue("role")]
	if !ok {
		notFound(w, "Role", 10011)
		return
	}
	writeJSON(w, http.StatusOK, role)
}

func applyRole(role *discord.Role, body map[string]json.RawMessage) {
	set(body, "name", &role.Name)
	set(body, "permissions", &role.Permissions)
	set(body, "color", &role.Color)
	if _, ok := body["colors"]; ok {
		var colors discord.RoleColors
		set(body, "colors", &colors)
		role.Color = colors.PrimaryColor
		role.Colors = &colors
	}
	if role.Colors == nil || role.Colors.PrimaryColor != role.Color {
		role.Colors = &discord.RoleColors{PrimaryColor: role.Color}
	}
	set(body, "hoist", &role.Hoist)
	set(body, "mentionable", &role.Mentionable)
	set(body, "unicode_emoji", &role.UnicodeEmoji)
}

func (s *Server) createRole(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.guild(w, r); !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	guildID := r.PathValue("guild")
	role := &discord.Role{ID: s.newID(), Name: "new role", Permissions: s.roles[guildID][guildID].Permissions, Position: 1, Colors: &discord.RoleColors{}}
	applyRole(role, body)
	for _, other := range s.roles[guildID] {
		if other.ID != guildID {
			other.Position++
		}
	}
	s.roles[guildID][role.ID] = role
	writeJSON(w, http.StatusOK, role)
}

func (s *Server) modifyRole(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	role, ok := s.roles[r.PathValue("guild")][r.PathValue("role")]
	if !ok {
		notFound(w, "Role", 10011)
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	applyRole(role, body)
	writeJSON(w, http.StatusOK, role)
}

func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	guildID, roleID := r.PathValue("guild"), r.PathValue("role")
	if _, ok := s.roles[guildID][roleID]; !ok || roleID == guildID {
		notFound(w, "Role", 10011)
		return
	}
	delete(s.roles[guildID], roleID)
	for _, m := range s.members[guildID] {
		m.Roles = slices.DeleteFunc(m.Roles, func(id string) bool { return id == roleID })
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) modifyRolePositions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	guildID := r.PathValue("guild")
	// Positions are stored as sent, so a moved role can tie with one that
	// was not moved; tied roles sort by ID, as Discord documents.
	var updates []discord.PositionUpdate
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	for _, u := range updates {
		role, ok := s.roles[guildID][u.ID]
		if !ok {
			notFound(w, "Role", 10011)
			return
		}
		role.Position = u.Position
	}
	writeJSON(w, http.StatusOK, s.sortedRoles(guildID))
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.guild(w, r); !ok {
		return
	}
	var out []*discord.Channel
	for _, ch := range s.channels {
		if ch.GuildID == r.PathValue("guild") && !s.hidden[ch.ID] {
			out = append(out, ch)
		}
	}
	slices.SortFunc(out, func(a, b *discord.Channel) int { return strings.Compare(a.ID, b.ID) })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) applyChannel(ch *discord.Channel, body map[string]json.RawMessage) error {
	set(body, "name", &ch.Name)
	set(body, "topic", &ch.Topic)
	set(body, "nsfw", &ch.NSFW)
	set(body, "rate_limit_per_user", &ch.RateLimitPerUser)
	set(body, "bitrate", &ch.Bitrate)
	set(body, "user_limit", &ch.UserLimit)
	set(body, "parent_id", &ch.ParentID)
	set(body, "rtc_region", &ch.RTCRegion)
	set(body, "video_quality_mode", &ch.VideoQualityMode)
	set(body, "default_auto_archive_duration", &ch.DefaultAutoArchiveDuration)
	set(body, "flags", &ch.Flags)
	set(body, "default_reaction_emoji", &ch.DefaultReactionEmoji)
	set(body, "default_thread_rate_limit_per_user", &ch.DefaultThreadRateLimitPerUser)
	set(body, "default_sort_order", &ch.DefaultSortOrder)
	set(body, "default_forum_layout", &ch.DefaultForumLayout)
	set(body, "position", &ch.Position)
	if ch.Topic != nil && *ch.Topic == "" {
		ch.Topic = nil
	}
	// Discord lowercases text channel names and replaces spaces with hyphens.
	if ch.Type == discord.ChannelTypeText || ch.Type == discord.ChannelTypeAnnouncement {
		ch.Name = strings.ToLower(strings.Join(strings.Fields(ch.Name), "-"))
	}
	if ch.Type == discord.ChannelTypeStage && ch.Bitrate > 64000 {
		return fmt.Errorf("bitrate %d exceeds 64000 for stage channels", ch.Bitrate)
	}
	if ch.ParentID != nil {
		parent, ok := s.channels[*ch.ParentID]
		if !ok || parent.Type != discord.ChannelTypeCategory {
			return fmt.Errorf("parent_id %s is not a category", *ch.ParentID)
		}
	}
	if _, ok := body["available_tags"]; ok {
		var tags []discord.ForumTag
		set(body, "available_tags", &tags)
		for i := range tags {
			if tags[i].ID == "" {
				tags[i].ID = s.newID()
			}
		}
		ch.AvailableTags = tags
	}
	return nil
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	ch := &discord.Channel{ID: s.newID(), GuildID: g.ID, PermissionOverwrites: []discord.Overwrite{}}
	set(body, "type", &ch.Type)
	if ch.Type == discord.ChannelTypeAnnouncement && !slices.Contains(g.Features, "NEWS") {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	switch ch.Type {
	case discord.ChannelTypeVoice, discord.ChannelTypeStage:
		ch.Bitrate = 64000
	case discord.ChannelTypeText, discord.ChannelTypeAnnouncement, discord.ChannelTypeForum, discord.ChannelTypeMedia:
		ch.DefaultAutoArchiveDuration = 4320
	}
	// Create Guild Channel does not list nsfw for media channels; model it as
	// ignored so only Modify Channel can set it.
	if ch.Type == discord.ChannelTypeMedia {
		delete(body, "nsfw")
	}
	if err := s.applyChannel(ch, body); err != nil {
		writeError(w, http.StatusBadRequest, 50035, err.Error())
		return
	}
	s.channels[ch.ID] = ch
	writeJSON(w, http.StatusCreated, ch)
}

func (s *Server) channel(w http.ResponseWriter, r *http.Request) (*discord.Channel, bool) {
	ch, ok := s.channels[r.PathValue("channel")]
	if !ok {
		notFound(w, "Channel", 10003)
	}
	return ch, ok
}

func (s *Server) getChannel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.denied[r.PathValue("channel")] {
		writeError(w, http.StatusForbidden, 50001, "Missing Access")
		return
	}
	if ch, ok := s.channel(w, r); ok {
		writeJSON(w, http.StatusOK, ch)
	}
}

// HideChannel omits a channel from GET /guilds/{guild}/channels while GET
// /channels/{id} still returns it, simulating a channel the bot cannot view.
func (s *Server) HideChannel(channelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hidden[channelID] = true
}

// DenyChannel hides a channel from the guild channel list and makes GET
// /channels/{id} return 403 Missing Access.
func (s *Server) DenyChannel(channelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hidden[channelID] = true
	s.denied[channelID] = true
}

func (s *Server) modifyChannel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channel(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	updated := *ch
	if err := s.applyChannel(&updated, body); err != nil {
		writeError(w, http.StatusBadRequest, 50035, err.Error())
		return
	}
	*ch = updated
	writeJSON(w, http.StatusOK, ch)
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channel(w, r)
	if !ok {
		return
	}
	delete(s.channels, ch.ID)
	for _, other := range s.channels {
		if other.ParentID != nil && *other.ParentID == ch.ID {
			other.ParentID = nil
		}
	}
	writeJSON(w, http.StatusOK, ch)
}

func (s *Server) modifyChannelPositions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var updates []discord.PositionUpdate
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	for _, u := range updates {
		ch, ok := s.channels[u.ID]
		if !ok || ch.GuildID != r.PathValue("guild") {
			notFound(w, "Channel", 10003)
			return
		}
		ch.Position = u.Position
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) editPermission(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channel(w, r)
	if !ok {
		return
	}
	var o discord.Overwrite
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	o.ID = r.PathValue("overwrite")
	ch.PermissionOverwrites = slices.DeleteFunc(ch.PermissionOverwrites, func(x discord.Overwrite) bool { return x.ID == o.ID })
	ch.PermissionOverwrites = append(ch.PermissionOverwrites, o)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deletePermission(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channel(w, r)
	if !ok {
		return
	}
	id := r.PathValue("overwrite")
	ch.PermissionOverwrites = slices.DeleteFunc(ch.PermissionOverwrites, func(x discord.Overwrite) bool { return x.ID == id })
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) member(w http.ResponseWriter, r *http.Request) (*discord.Member, bool) {
	m, ok := s.members[r.PathValue("guild")][r.PathValue("user")]
	if !ok {
		notFound(w, "Member", 10007)
	}
	return m, ok
}

func (s *Server) getMember(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.member(w, r); ok {
		writeJSON(w, http.StatusOK, m)
	}
}

func (s *Server) searchMembers(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	query := strings.ToLower(r.URL.Query().Get("query"))
	out := []*discord.Member{}
	for _, m := range s.members[r.PathValue("guild")] {
		if strings.HasPrefix(strings.ToLower(m.User.Username), query) || (m.Nick != nil && strings.HasPrefix(strings.ToLower(*m.Nick), query)) {
			out = append(out, m)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) addMemberRole(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.member(w, r)
	if !ok {
		return
	}
	roleID := r.PathValue("role")
	if _, ok := s.roles[r.PathValue("guild")][roleID]; !ok {
		notFound(w, "Role", 10011)
		return
	}
	if !slices.Contains(m.Roles, roleID) {
		m.Roles = append(m.Roles, roleID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeMemberRole(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.member(w, r)
	if !ok {
		return
	}
	roleID := r.PathValue("role")
	m.Roles = slices.DeleteFunc(m.Roles, func(id string) bool { return id == roleID })
	w.WriteHeader(http.StatusNoContent)
}

// RemoveMemberRole revokes a role outside of the API, simulating drift.
func (s *Server) RemoveMemberRole(guildID, userID, roleID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.members[guildID][userID]; ok {
		m.Roles = slices.DeleteFunc(m.Roles, func(id string) bool { return id == roleID })
	}
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channel(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	wh := &discord.Webhook{ID: s.newID(), Type: 1, GuildID: ch.GuildID, ChannelID: ch.ID, Token: "tok" + s.newID()}
	set(body, "name", &wh.Name)
	if wh.Name == nil || !validWebhookName(*wh.Name) {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	s.setAvatar(wh, body)
	s.webhooks[wh.ID] = wh
	writeJSON(w, http.StatusOK, wh)
}

func validWebhookName(name string) bool {
	lower := strings.ToLower(name)
	return name != "" && len(name) <= 80 && !strings.Contains(lower, "clyde") && !strings.Contains(lower, "discord")
}

func (s *Server) setAvatar(wh *discord.Webhook, body map[string]json.RawMessage) {
	if _, ok := body["avatar"]; !ok {
		return
	}
	var avatar *string
	set(body, "avatar", &avatar)
	if avatar != nil {
		h := "avatar" + s.newID()
		avatar = &h
	}
	wh.Avatar = avatar
}

func (s *Server) getWebhook(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wh, ok := s.webhooks[r.PathValue("webhook")]
	if !ok {
		notFound(w, "Webhook", 10015)
		return
	}
	writeJSON(w, http.StatusOK, wh)
}

func (s *Server) modifyWebhook(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wh, ok := s.webhooks[r.PathValue("webhook")]
	if !ok {
		notFound(w, "Webhook", 10015)
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	updated := *wh
	set(body, "name", &updated.Name)
	if updated.Name == nil || !validWebhookName(*updated.Name) {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	*wh = updated
	set(body, "channel_id", &wh.ChannelID)
	s.setAvatar(wh, body)
	writeJSON(w, http.StatusOK, wh)
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.webhooks[r.PathValue("webhook")]; !ok {
		notFound(w, "Webhook", 10015)
		return
	}
	delete(s.webhooks, r.PathValue("webhook"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channel(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	inv := &discord.Invite{Code: "inv" + s.newID(), Channel: &discord.InviteChannel{ID: ch.ID}, MaxAge: 86400, CreatedAt: "2024-01-01T00:00:00+00:00"}
	set(body, "max_age", &inv.MaxAge)
	set(body, "max_uses", &inv.MaxUses)
	set(body, "temporary", &inv.Temporary)
	var unique bool
	set(body, "unique", &unique)
	if !unique {
		for _, existing := range s.invites {
			if existing.Channel.ID == ch.ID && existing.MaxAge == inv.MaxAge && existing.MaxUses == inv.MaxUses && existing.Temporary == inv.Temporary {
				writeJSON(w, http.StatusOK, existing)
				return
			}
		}
	}
	if inv.MaxAge > 0 {
		expires := "2099-01-01T00:00:00+00:00"
		inv.ExpiresAt = &expires
	}
	s.invites[inv.Code] = inv
	writeJSON(w, http.StatusOK, inv)
}

func (s *Server) listChannelInvites(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channel(w, r)
	if !ok {
		return
	}
	out := []*discord.Invite{}
	for _, inv := range s.invites {
		if inv.Channel.ID == ch.ID {
			out = append(out, inv)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteInvite(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invites[r.PathValue("code")]
	if !ok {
		notFound(w, "Invite", 10006)
		return
	}
	delete(s.invites, inv.Code)
	writeJSON(w, http.StatusOK, inv)
}

// DeleteInvite removes an invite outside of the API, simulating expiry.
func (s *Server) DeleteInvite(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.invites, code)
}

func (s *Server) message(w http.ResponseWriter, r *http.Request) (*discord.Message, bool) {
	m, ok := s.messages[r.PathValue("message")]
	if !ok || m.ChannelID != r.PathValue("channel") {
		notFound(w, "Message", 10008)
		return nil, false
	}
	return m, true
}

// applyMessage mimics Discord: text is trimmed, sent embeds become "rich"
// embeds, and a link in the content is unfurled into an "article" embed.
func applyMessage(m *discord.Message, body map[string]json.RawMessage) {
	set(body, "content", &m.Content)
	m.Content = strings.TrimSpace(m.Content)
	if _, ok := body["embeds"]; ok {
		set(body, "embeds", &m.Embeds)
		for i := range m.Embeds {
			e := &m.Embeds[i]
			e.Type = "rich"
			e.Title = strings.TrimSpace(e.Title)
			e.Description = strings.TrimSpace(e.Description)
			if e.Footer != nil {
				e.Footer.Text = strings.TrimSpace(e.Footer.Text)
			}
			for j := range e.Fields {
				e.Fields[j].Name = strings.TrimSpace(e.Fields[j].Name)
				e.Fields[j].Value = strings.TrimSpace(e.Fields[j].Value)
			}
		}
	}
	m.Embeds = slices.DeleteFunc(m.Embeds, func(e discord.Embed) bool { return e.Type != "rich" })
	for _, word := range strings.Fields(m.Content) {
		if strings.HasPrefix(word, "https://") {
			m.Embeds = append(m.Embeds, discord.Embed{Type: "article", URL: word, Title: "Preview"})
		}
	}
}

// MessageEdits returns the request bodies of every message edit so far.
func (s *Server) MessageEdits() []map[string]json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.edits)
}

func (s *Server) createMessage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channel(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	m := &discord.Message{ID: s.newID(), ChannelID: ch.ID, Author: &discord.User{ID: s.botUserID, Username: "bot", Bot: true}, Embeds: []discord.Embed{}}
	applyMessage(m, body)
	if m.Content == "" && len(m.Embeds) == 0 {
		writeError(w, http.StatusBadRequest, 50006, "Cannot send an empty message")
		return
	}
	s.messages[m.ID] = m
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.message(w, r); ok {
		writeJSON(w, http.StatusOK, m)
	}
}

func (s *Server) editMessage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.message(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	s.edits = append(s.edits, body)
	applyMessage(m, body)
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.message(w, r)
	if !ok {
		return
	}
	delete(s.messages, m.ID)
	w.WriteHeader(http.StatusNoContent)
}

// DeleteMessage removes a message outside of the API, simulating a moderator
// deleting it.
func (s *Server) DeleteMessage(messageID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.messages, messageID)
}

func (s *Server) pinMessage(pinned bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		m, ok := s.message(w, r)
		if !ok {
			return
		}
		m.Pinned = pinned
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) getEmoji(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.emojis[r.PathValue("guild")][r.PathValue("emoji")]
	if !ok {
		notFound(w, "Emoji", 10014)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) createEmoji(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.guild(w, r); !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	var image string
	set(body, "image", &image)
	if !strings.HasPrefix(image, "data:image/") {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	e := &discord.Emoji{ID: s.newID(), Roles: []string{}, Animated: strings.HasPrefix(image, "data:image/gif")}
	set(body, "name", &e.Name)
	set(body, "roles", &e.Roles)
	s.emojis[r.PathValue("guild")][e.ID] = e
	writeJSON(w, http.StatusCreated, e)
}

func (s *Server) modifyEmoji(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.emojis[r.PathValue("guild")][r.PathValue("emoji")]
	if !ok {
		notFound(w, "Emoji", 10014)
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	set(body, "name", &e.Name)
	set(body, "roles", &e.Roles)
	if e.Roles == nil {
		e.Roles = []string{}
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) deleteEmoji(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.emojis[r.PathValue("guild")][r.PathValue("emoji")]; !ok {
		notFound(w, "Emoji", 10014)
		return
	}
	delete(s.emojis[r.PathValue("guild")], r.PathValue("emoji"))
	w.WriteHeader(http.StatusNoContent)
}
