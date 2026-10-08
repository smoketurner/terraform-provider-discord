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
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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

	mu       sync.Mutex
	nextID   uint64
	guilds   map[string]*discord.Guild
	roles    map[string]map[string]*discord.Role
	channels map[string]*discord.Channel
	threads  map[string]*discord.Thread
	members  map[string]map[string]*discord.Member
	bans     map[string]map[string]*discord.Ban
	webhooks map[string]*discord.Webhook
	posters  map[string]string // message ID to the webhook that posted it
	invites  map[string]*discord.Invite
	messages map[string]*discord.Message
	emojis   map[string]map[string]*discord.Emoji
	automod  map[string]map[string]*discord.AutoModerationRule
	stickers map[string]map[string]*discord.Sticker
	sounds   map[string]map[string]*discord.SoundboardSound
	events   map[string]*discord.ScheduledEvent
	stages   map[string]*discord.StageInstance
	settings map[string]*guildSettings
	ro       *readOnlyState
	money    *monetization
	app      *discord.Application
	commands map[string]*discord.ApplicationCommand
	// stickerFiles and soundData hold the uploaded files, which Discord
	// never returns.
	stickerFiles map[string]Upload
	soundData    map[string]string
	requests     []string
	headers      []http.Header
	edits        []map[string]json.RawMessage
	failNext     map[string]int
	botUserID    string
	// hidden channels are omitted from the guild channel list, as Discord
	// does for channels the bot lacks VIEW_CHANNEL on. denied channels are
	// also refused by GET /channels/{id}.
	hidden map[string]bool
	denied map[string]bool
	// follows maps Channel Follower webhook IDs to the announcement channel
	// each follows.
	follows map[string]string
	// reactions maps message IDs to emoji to the sorted IDs of the users who
	// reacted.
	reactions map[string]map[string][]string
	// clockSkew is added to the wall clock, so tests can let incident
	// actions expire without waiting.
	clockSkew time.Duration
}

// NewServer starts a fake Discord API seeded with one guild containing an
// @everyone role and one member. Call Close when done.
func NewServer() *Server {
	s := &Server{
		nextID:       200000000000000000,
		guilds:       map[string]*discord.Guild{},
		roles:        map[string]map[string]*discord.Role{},
		channels:     map[string]*discord.Channel{},
		threads:      map[string]*discord.Thread{},
		members:      map[string]map[string]*discord.Member{},
		bans:         map[string]map[string]*discord.Ban{GuildID: {}},
		webhooks:     map[string]*discord.Webhook{},
		posters:      map[string]string{},
		invites:      map[string]*discord.Invite{},
		messages:     map[string]*discord.Message{},
		emojis:       map[string]map[string]*discord.Emoji{},
		automod:      map[string]map[string]*discord.AutoModerationRule{},
		stickers:     map[string]map[string]*discord.Sticker{},
		sounds:       map[string]map[string]*discord.SoundboardSound{},
		stickerFiles: map[string]Upload{},
		soundData:    map[string]string{},
		events:       map[string]*discord.ScheduledEvent{},
		stages:       map[string]*discord.StageInstance{},
		failNext:     map[string]int{},
		botUserID:    "100000000000000003",
		hidden:       map[string]bool{},
		denied:       map[string]bool{},
		follows:      map[string]string{},
		reactions:    map[string]map[string][]string{},
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
	s.stickers[GuildID] = map[string]*discord.Sticker{}
	s.sounds[GuildID] = map[string]*discord.SoundboardSound{}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /guilds/{guild}", s.getGuild)
	mux.HandleFunc("PATCH /guilds/{guild}", s.modifyGuild)
	mux.HandleFunc("PUT /guilds/{guild}/incident-actions", s.modifyIncidentActions)
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
	mux.HandleFunc("POST /channels/{channel}/threads", s.startThread)
	mux.HandleFunc("POST /channels/{channel}/messages/{message}/threads", s.startThreadFromMessage)
	mux.HandleFunc("PUT /channels/{channel}/permissions/{overwrite}", s.editPermission)
	mux.HandleFunc("DELETE /channels/{channel}/permissions/{overwrite}", s.deletePermission)
	mux.HandleFunc("GET /guilds/{guild}/members/search", s.searchMembers)
	mux.HandleFunc("GET /guilds/{guild}/members/{user}", s.getMember)
	mux.HandleFunc("PATCH /guilds/{guild}/members/{user}", s.modifyMember)
	mux.HandleFunc("PUT /guilds/{guild}/members/{user}/roles/{role}", s.addMemberRole)
	mux.HandleFunc("DELETE /guilds/{guild}/members/{user}/roles/{role}", s.removeMemberRole)
	mux.HandleFunc("GET /guilds/{guild}/bans/{user}", s.getBan)
	mux.HandleFunc("PUT /guilds/{guild}/bans/{user}", s.createBan)
	mux.HandleFunc("DELETE /guilds/{guild}/bans/{user}", s.removeBan)
	mux.HandleFunc("POST /channels/{channel}/webhooks", s.createWebhook)
	mux.HandleFunc("GET /webhooks/{webhook}", s.getWebhook)
	mux.HandleFunc("PATCH /webhooks/{webhook}", s.modifyWebhook)
	mux.HandleFunc("DELETE /webhooks/{webhook}", s.deleteWebhook)
	mux.HandleFunc("POST /webhooks/{webhook}/{token}", s.executeWebhook)
	mux.HandleFunc("GET /webhooks/{webhook}/{token}/messages/{message}", s.getWebhookMessage)
	mux.HandleFunc("PATCH /webhooks/{webhook}/{token}/messages/{message}", s.editWebhookMessage)
	mux.HandleFunc("DELETE /webhooks/{webhook}/{token}/messages/{message}", s.deleteWebhookMessage)
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
	mux.HandleFunc("GET /guilds/{guild}/stickers/{sticker}", s.getSticker)
	mux.HandleFunc("POST /guilds/{guild}/stickers", s.createSticker)
	mux.HandleFunc("PATCH /guilds/{guild}/stickers/{sticker}", s.modifySticker)
	mux.HandleFunc("DELETE /guilds/{guild}/stickers/{sticker}", s.deleteSticker)
	mux.HandleFunc("GET /guilds/{guild}/soundboard-sounds", s.listSoundboardSounds)
	mux.HandleFunc("GET /guilds/{guild}/soundboard-sounds/{sound}", s.getSoundboardSound)
	mux.HandleFunc("POST /guilds/{guild}/soundboard-sounds", s.createSoundboardSound)
	mux.HandleFunc("PATCH /guilds/{guild}/soundboard-sounds/{sound}", s.modifySoundboardSound)
	mux.HandleFunc("DELETE /guilds/{guild}/soundboard-sounds/{sound}", s.deleteSoundboardSound)
	mux.HandleFunc("GET /guilds/{guild}/auto-moderation/rules/{rule}", s.getAutomodRule)
	mux.HandleFunc("POST /guilds/{guild}/auto-moderation/rules", s.createAutomodRule)
	mux.HandleFunc("PATCH /guilds/{guild}/auto-moderation/rules/{rule}", s.modifyAutomodRule)
	mux.HandleFunc("DELETE /guilds/{guild}/auto-moderation/rules/{rule}", s.deleteAutomodRule)
	mux.HandleFunc("GET /guilds/{guild}/scheduled-events/{event}", s.getScheduledEvent)
	mux.HandleFunc("POST /guilds/{guild}/scheduled-events", s.createScheduledEvent)
	mux.HandleFunc("PATCH /guilds/{guild}/scheduled-events/{event}", s.modifyScheduledEvent)
	mux.HandleFunc("DELETE /guilds/{guild}/scheduled-events/{event}", s.deleteScheduledEvent)
	mux.HandleFunc("GET /stage-instances/{channel}", s.getStageInstance)
	mux.HandleFunc("POST /stage-instances", s.createStageInstance)
	mux.HandleFunc("PATCH /stage-instances/{channel}", s.modifyStageInstance)
	mux.HandleFunc("DELETE /stage-instances/{channel}", s.deleteStageInstance)
	s.handleGuildSettings(mux)
	s.handleReactions(mux)
	s.handleFollowers(mux)
	s.handleReadOnly(mux)
	s.handleMonetization(mux)
	s.handleUsers(mux)
	s.handleApplicationCommands(mux)

	s.Server = httptest.NewServer(s.middleware(mux))
	return s
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Webhook token endpoints are authenticated by the token in the path.
		if !webhookTokenPath(r.URL.Path) && r.Header.Get("Authorization") != "Bot "+Token {
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
		s.expireIncidentActions(g)
		writeJSON(w, http.StatusOK, g)
	}
}

// mutableGuildFeatures are the features Modify Guild can add or remove.
var mutableGuildFeatures = []string{"COMMUNITY", "DISCOVERABLE", "INVITES_DISABLED", "RAID_ALERTS_DISABLED"}

// setFeatures applies a features array. Discord grants the other features,
// so the fake rejects a request that adds or removes one of them.
func setFeatures(g *discord.Guild, features []string) string {
	for _, f := range slices.Concat(g.Features, features) {
		if !slices.Contains(mutableGuildFeatures, f) && slices.Contains(g.Features, f) != slices.Contains(features, f) {
			return "feature " + f + " cannot be added or removed"
		}
	}
	if slices.Contains(features, "COMMUNITY") && !slices.Contains(g.Features, "COMMUNITY") &&
		(g.RulesChannelID == nil || g.PublicUpdatesChannelID == nil) {
		return "COMMUNITY requires a rules channel and a public updates channel"
	}
	g.Features = features
	return ""
}

// setImage stores a fake hash for an uploaded image, or clears it on null.
func (s *Server) setImage(body map[string]json.RawMessage, key string, dst **string) {
	if _, ok := body[key]; !ok {
		return
	}
	var image *string
	set(body, key, &image)
	if image != nil {
		h := key + s.newID()
		image = &h
	}
	*dst = image
}

func (s *Server) modifyGuild(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.guild(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	// Changes go to a copy so that a rejected request changes nothing.
	g := *cur
	s.setImage(body, "icon", &g.Icon)
	s.setImage(body, "banner", &g.Banner)
	s.setImage(body, "splash", &g.Splash)
	s.setImage(body, "discovery_splash", &g.DiscoverySplash)
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
	if _, ok := body["features"]; ok {
		var features []string
		set(body, "features", &features)
		if msg := setFeatures(&g, features); msg != "" {
			writeError(w, http.StatusBadRequest, 50035, msg)
			return
		}
	}
	*cur = g
	writeJSON(w, http.StatusOK, cur)
}

func (s *Server) now() time.Time {
	return time.Now().Add(s.clockSkew)
}

// AdvanceClock moves the fake's clock forward, expiring incident actions.
func (s *Server) AdvanceClock(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clockSkew += d
}

// expireIncidentActions clears the incident actions that have ended, as
// Discord does.
func (s *Server) expireIncidentActions(g *discord.Guild) {
	if g.IncidentsData == nil {
		return
	}
	for _, until := range []**string{&g.IncidentsData.InvitesDisabledUntil, &g.IncidentsData.DMsDisabledUntil} {
		if *until == nil {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, **until); err == nil && !t.After(s.now()) {
			*until = nil
		}
	}
}

func (s *Server) modifyIncidentActions(w http.ResponseWriter, r *http.Request) {
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
	s.expireIncidentActions(g)
	if g.IncidentsData == nil {
		g.IncidentsData = &discord.IncidentsData{}
	}
	d := *g.IncidentsData
	for key, until := range map[string]**string{"invites_disabled_until": &d.InvitesDisabledUntil, "dms_disabled_until": &d.DMsDisabledUntil} {
		set(body, key, until)
		if *until == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, **until)
		if err != nil || !t.After(s.now()) || t.After(s.now().Add(24*time.Hour)) {
			writeError(w, http.StatusBadRequest, 50035, key+" must be a timestamp within the next 24 hours")
			return
		}
		// Discord returns timestamps in its own format.
		formatted := t.UTC().Format("2006-01-02T15:04:05.000000+00:00")
		*until = &formatted
	}
	g.IncidentsData = &d
	writeJSON(w, http.StatusOK, g.IncidentsData)
}

// RemoveGuild deletes a guild, as when the bot is removed from it.
func (s *Server) RemoveGuild(guildID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.guilds, guildID)
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

// applyRole applies a role body. Like Discord, it rejects a role icon or
// emoji unless the guild has the ROLE_ICONS feature, and stores a new hash
// for every uploaded icon.
func (s *Server) applyRole(w http.ResponseWriter, g *discord.Guild, role *discord.Role, body map[string]json.RawMessage) bool {
	var icon, emoji *string
	set(body, "icon", &icon)
	set(body, "unicode_emoji", &emoji)
	if (icon != nil || emoji != nil) && !slices.Contains(g.Features, "ROLE_ICONS") {
		writeError(w, http.StatusBadRequest, 50101, "This server needs more boosts to perform this action")
		return false
	}
	if _, ok := body["icon"]; ok {
		if icon != nil {
			h := "roleicon" + s.newID()
			icon = &h
		}
		role.Icon = icon
	}
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
	return true
}

func (s *Server) createRole(w http.ResponseWriter, r *http.Request) {
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
	guildID := r.PathValue("guild")
	role := &discord.Role{ID: s.newID(), Name: "new role", Permissions: s.roles[guildID][guildID].Permissions, Position: 1, Colors: &discord.RoleColors{}}
	if !s.applyRole(w, g, role, body) {
		return
	}
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
	if s.applyRole(w, s.guilds[r.PathValue("guild")], role, body) {
		writeJSON(w, http.StatusOK, role)
	}
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
	var overwrites []struct {
		ID    string  `json:"id"`
		Type  *int    `json:"type"`
		Allow *string `json:"allow"`
		Deny  *string `json:"deny"`
	}
	set(body, "permission_overwrites", &overwrites)
	for _, o := range overwrites {
		if o.Type == nil || (*o.Type != 0 && *o.Type != 1) {
			writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
			return
		}
		// Discord defaults an omitted or null allow and deny to "0".
		ow := discord.Overwrite{ID: o.ID, Type: *o.Type, Allow: "0", Deny: "0"}
		if o.Allow != nil {
			ow.Allow = *o.Allow
		}
		if o.Deny != nil {
			ow.Deny = *o.Deny
		}
		ch.PermissionOverwrites = append(ch.PermissionOverwrites, ow)
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
	if t, ok := s.threads[r.PathValue("channel")]; ok {
		writeJSON(w, http.StatusOK, t)
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

// SetGuildFeatures replaces the features of the seeded guild.
func (s *Server) SetGuildFeatures(features ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.guilds[GuildID].Features = features
}

func (s *Server) modifyChannel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.threads[r.PathValue("channel")]; ok {
		s.modifyThread(w, r, t)
		return
	}
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
	if _, ok := body["type"]; ok {
		set(body, "type", &updated.Type)
		// Only text and announcement channels convert, and only in guilds
		// with the NEWS feature.
		convertible := func(t int) bool { return t == discord.ChannelTypeText || t == discord.ChannelTypeAnnouncement }
		if updated.Type != ch.Type && (!convertible(ch.Type) || !convertible(updated.Type) ||
			!slices.Contains(s.guilds[ch.GuildID].Features, "NEWS")) {
			writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
			return
		}
	}
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
	if t, ok := s.threads[r.PathValue("channel")]; ok {
		s.deleteThread(t.ID)
		writeJSON(w, http.StatusOK, t)
		return
	}
	ch, ok := s.channel(w, r)
	if !ok {
		return
	}
	delete(s.channels, ch.ID)
	delete(s.stages, ch.ID)
	for _, t := range s.threads {
		if t.ParentID != nil && *t.ParentID == ch.ID {
			s.deleteThread(t.ID)
		}
	}
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

// maxTimeout is how far ahead Discord accepts communication_disabled_until.
const maxTimeout = 28 * 24 * time.Hour

func (s *Server) modifyMember(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.member(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	guildID, userID := r.PathValue("guild"), r.PathValue("user")
	nick := m.Nick
	set(body, "nick", &nick)
	if nick != nil && *nick == "" {
		nick = nil
	}
	if nick != nil && utf8.RuneCountInString(*nick) > 32 {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: nick must be 32 or fewer in length")
		return
	}
	roles := m.Roles
	if _, ok := body["roles"]; ok {
		var ids []string
		set(body, "roles", &ids)
		if msg := s.checkMemberRoles(guildID, m.Roles, ids); msg != "" {
			writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: "+msg)
			return
		}
		roles = slices.Compact(slices.Sorted(slices.Values(ids)))
	}
	until := m.CommunicationDisabledUntil
	if _, ok := body["communication_disabled_until"]; ok {
		// Discord refuses to time out the owner and administrators.
		if userID == s.guilds[guildID].OwnerID {
			writeError(w, http.StatusForbidden, 50013, "Missing Permissions")
			return
		}
		set(body, "communication_disabled_until", &until)
		if until != nil {
			t, err := time.Parse(time.RFC3339, *until)
			if err != nil || t.After(time.Now().Add(maxTimeout)) {
				writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: communication_disabled_until must be at most 28 days in the future")
				return
			}
		}
	}
	m.Nick, m.Roles, m.CommunicationDisabledUntil = nick, roles, until
	writeJSON(w, http.StatusOK, m)
}

// checkMemberRoles describes why a member's role list cannot change from
// current to next, or returns "". @everyone is implicit, and managed roles
// can only be granted and revoked by their integration.
func (s *Server) checkMemberRoles(guildID string, current, next []string) string {
	for _, id := range next {
		role, ok := s.roles[guildID][id]
		switch {
		case !ok || id == guildID:
			return "unknown role " + id
		case role.Managed && !slices.Contains(current, id):
			return "cannot add managed role " + id
		}
	}
	for _, id := range current {
		if role, ok := s.roles[guildID][id]; ok && role.Managed && !slices.Contains(next, id) {
			return "cannot remove managed role " + id
		}
	}
	return ""
}

// AddMember adds a member with no roles to a guild and returns its user ID.
func (s *Server) AddMember(guildID, username string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.newID()
	s.members[guildID][id] = &discord.Member{
		User:     &discord.User{ID: id, Username: username, Discriminator: "0"},
		Roles:    []string{},
		JoinedAt: "2024-01-01T00:00:00.000000+00:00",
	}
	return id
}

// RemoveMember removes a member from a guild, as when they leave.
func (s *Server) RemoveMember(guildID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.members[guildID], userID)
}

func (s *Server) getBan(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.guild(w, r); !ok {
		return
	}
	b, ok := s.bans[r.PathValue("guild")][r.PathValue("user")]
	if !ok {
		notFound(w, "Ban", 10026)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// maxBanDeleteMessageSeconds is the most message history, 7 days, a ban can
// delete.
const maxBanDeleteMessageSeconds = 604800

// createBan bans a user and, as Discord does, removes them from the guild.
// The ban's reason is the decoded X-Audit-Log-Reason header. Discord refuses
// to ban the guild owner.
func (s *Server) createBan(w http.ResponseWriter, r *http.Request) {
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
	var seconds int
	set(body, "delete_message_seconds", &seconds)
	if seconds < 0 || seconds > maxBanDeleteMessageSeconds {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: delete_message_seconds must be between 0 and 604800")
		return
	}
	userID := r.PathValue("user")
	if userID == g.OwnerID {
		writeError(w, http.StatusForbidden, 50013, "Missing Permissions")
		return
	}
	user := &discord.User{ID: userID, Username: "user" + userID, Discriminator: "0"}
	if m, ok := s.members[g.ID][userID]; ok {
		user = m.User
	}
	var reason *string
	if h := r.Header.Get("X-Audit-Log-Reason"); h != "" {
		decoded, err := url.PathUnescape(h)
		if err != nil {
			writeError(w, http.StatusBadRequest, 50035, "Invalid X-Audit-Log-Reason header")
			return
		}
		reason = &decoded
	}
	s.bans[g.ID][userID] = &discord.Ban{Reason: reason, User: user}
	delete(s.members[g.ID], userID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeBan(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.guild(w, r); !ok {
		return
	}
	guildID, userID := r.PathValue("guild"), r.PathValue("user")
	if _, ok := s.bans[guildID][userID]; !ok {
		notFound(w, "Ban", 10026)
		return
	}
	delete(s.bans[guildID], userID)
	w.WriteHeader(http.StatusNoContent)
}

// AddManagedRole creates a role managed by an integration, such as a bot's
// role, grants it to the given members and returns its ID.
func (s *Server) AddManagedRole(guildID, name string, userIDs ...string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	role := &discord.Role{ID: s.newID(), Name: name, Permissions: "0", Position: 1, Managed: true, Colors: &discord.RoleColors{}}
	s.roles[guildID][role.ID] = role
	for _, userID := range userIDs {
		m := s.members[guildID][userID]
		m.Roles = append(m.Roles, role.ID)
	}
	return role.ID
}

// SetMemberTimeout sets a member's communication_disabled_until outside of
// the API, as when a timeout expires or a moderator changes it.
func (s *Server) SetMemberTimeout(guildID, userID string, until *string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.members[guildID][userID]; ok {
		m.CommunicationDisabledUntil = until
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
	writeJSON(w, http.StatusOK, s.webhookResponse(wh))
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
	writeJSON(w, http.StatusOK, s.webhookResponse(wh))
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.webhooks[r.PathValue("webhook")]; !ok {
		notFound(w, "Webhook", 10015)
		return
	}
	delete(s.webhooks, r.PathValue("webhook"))
	delete(s.follows, r.PathValue("webhook"))
	w.WriteHeader(http.StatusNoContent)
}

// AddChannelFollowerWebhook adds a Channel Follower webhook to a channel.
// Discord creates these when a channel follows an announcement channel, and
// they have no token.
func (s *Server) AddChannelFollowerWebhook(channelID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := "Followed"
	wh := &discord.Webhook{ID: s.newID(), Type: 2, GuildID: s.channels[channelID].GuildID, ChannelID: channelID, Name: &name}
	s.webhooks[wh.ID] = wh
	return wh.ID
}

// webhookTokenPath reports whether a path is /webhooks/{id}/{token} or below.
func webhookTokenPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) >= 3 && parts[0] == "webhooks"
}

// tokenWebhook returns the webhook a webhook token endpoint names, checking
// the token in the path.
func (s *Server) tokenWebhook(w http.ResponseWriter, r *http.Request) (*discord.Webhook, bool) {
	wh, ok := s.webhooks[r.PathValue("webhook")]
	if !ok {
		notFound(w, "Webhook", 10015)
		return nil, false
	}
	if wh.Token == "" || wh.Token != r.PathValue("token") {
		writeError(w, http.StatusUnauthorized, 50027, "Invalid Webhook Token")
		return nil, false
	}
	return wh, true
}

// executeWebhook models Execute Webhook: a forum or media webhook posts in
// the thread_id thread or starts a post named thread_name, and any other
// webhook posts in its channel or a thread of it.
func (s *Server) executeWebhook(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wh, ok := s.tokenWebhook(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	parent := s.channels[wh.ChannelID]
	forum := parent.Type == discord.ChannelTypeForum || parent.Type == discord.ChannelTypeMedia
	var threadName, username string
	set(body, "thread_name", &threadName)
	set(body, "username", &username)
	if username == "" {
		username = *wh.Name
	}
	m := &discord.Message{ID: s.newID(), ChannelID: wh.ChannelID, Author: &discord.User{ID: wh.ID, Username: username, Bot: true}, Embeds: []discord.Embed{}}
	applyMessage(m, body)
	if m.Content == "" && len(m.Embeds) == 0 {
		writeError(w, http.StatusBadRequest, 50006, "Cannot send an empty message")
		return
	}
	threadID := r.URL.Query().Get("thread_id")
	switch {
	case threadID != "" && threadName != "":
		writeError(w, http.StatusBadRequest, 50035, "thread_name cannot be used with thread_id")
		return
	case threadID != "":
		t, ok := s.threads[threadID]
		if !ok || *t.ParentID != wh.ChannelID {
			notFound(w, "Channel", 10003)
			return
		}
		t.ThreadMetadata.Archived = false
		m.ChannelID = t.ID
	case threadName != "":
		if !forum {
			writeError(w, http.StatusBadRequest, 220003, "Webhooks can only create threads in forum channels")
			return
		}
		t := s.newThread(m.ID, parent, discord.ChannelTypePublicThread)
		t.Name = threadName
		if len(threadName) > 100 {
			writeError(w, http.StatusBadRequest, 50035, "thread_name must be 1-100 characters")
			return
		}
		s.threads[t.ID] = t
		m.ChannelID = t.ID
	case forum:
		writeError(w, http.StatusBadRequest, 220001, "Webhooks posted to forum channels must have a thread_name or thread_id")
		return
	}
	s.messages[m.ID] = m
	s.posters[m.ID] = wh.ID
	if r.URL.Query().Get("wait") != "true" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// webhookMessage returns the message a webhook message endpoint names. It
// must have been posted by the webhook, in the thread_id thread if given or
// else in the webhook's channel.
func (s *Server) webhookMessage(w http.ResponseWriter, r *http.Request) (*discord.Message, bool) {
	wh, ok := s.tokenWebhook(w, r)
	if !ok {
		return nil, false
	}
	channelID := wh.ChannelID
	if id := r.URL.Query().Get("thread_id"); id != "" {
		channelID = id
	}
	m, ok := s.messages[r.PathValue("message")]
	if !ok || s.posters[m.ID] != wh.ID || m.ChannelID != channelID {
		notFound(w, "Message", 10008)
		return nil, false
	}
	return m, true
}

func (s *Server) getWebhookMessage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.webhookMessage(w, r); ok {
		writeJSON(w, http.StatusOK, m)
	}
}

func (s *Server) editWebhookMessage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.webhookMessage(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	if t, ok := s.threads[m.ChannelID]; ok && t.ThreadMetadata.Archived {
		writeError(w, http.StatusBadRequest, 50083, "Thread is archived")
		return
	}
	s.edits = append(s.edits, body)
	applyMessage(m, body)
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) deleteWebhookMessage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.webhookMessage(w, r)
	if !ok {
		return
	}
	delete(s.messages, m.ID)
	delete(s.posters, m.ID)
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
	m := &discord.Message{
		ID: s.newID(), ChannelID: ch.ID, Author: &discord.User{ID: s.botUserID, Username: "bot", Bot: true}, Embeds: []discord.Embed{},
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}
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
	if t, ok := s.threads[m.ChannelID]; ok && t.ThreadMetadata.Archived {
		writeError(w, http.StatusBadRequest, 50083, "Thread is archived")
		return
	}
	s.edits = append(s.edits, body)
	applyMessage(m, body)
	edited := time.Now().UTC().Format(time.RFC3339Nano)
	m.EditedTimestamp = &edited
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
	delete(s.reactions, m.ID)
	w.WriteHeader(http.StatusNoContent)
}

// DeleteMessage removes a message outside of the API, simulating a moderator
// deleting it.
func (s *Server) DeleteMessage(messageID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.messages, messageID)
	delete(s.reactions, messageID)
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
		s.recordPin(m, pinned)
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

// stickerFormats maps the content type of an uploaded sticker file to its
// format type. An image/png file with an animation control chunk is an APNG.
var stickerFormats = map[string]int{"image/png": 1, "image/gif": 4, "application/json": 3}

// StickerUpload returns the file uploaded for a sticker.
func (s *Server) StickerUpload(stickerID string) (Upload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.stickerFiles[stickerID]
	return u, ok
}

func (s *Server) getSticker(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.stickers[r.PathValue("guild")][r.PathValue("sticker")]
	if !ok {
		notFound(w, "Sticker", 10060)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) createSticker(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	if mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mediaType != "multipart/form-data" {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	var file Upload
	set(body, "file", &file)
	format, ok := stickerFormats[file.ContentType]
	if !ok || len(file.Data) == 0 || len(file.Data) > 512<<10 {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	if format == 3 && !slices.Contains(g.Features, "VERIFIED") && !slices.Contains(g.Features, "PARTNERED") {
		writeError(w, http.StatusBadRequest, 50035, "Lottie stickers need the VERIFIED or PARTNERED feature")
		return
	}
	if format == 1 && strings.Contains(string(file.Data), "acTL") {
		format = 2
	}
	st := &discord.Sticker{ID: s.newID(), FormatType: format, Available: true, GuildID: g.ID}
	set(body, "name", &st.Name)
	set(body, "tags", &st.Tags)
	set(body, "description", &st.Description)
	if st.Name == "" || st.Tags == "" {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	s.stickers[g.ID][st.ID] = st
	s.stickerFiles[st.ID] = file
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) modifySticker(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.stickers[r.PathValue("guild")][r.PathValue("sticker")]
	if !ok {
		notFound(w, "Sticker", 10060)
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	set(body, "name", &st.Name)
	set(body, "tags", &st.Tags)
	set(body, "description", &st.Description)
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) deleteSticker(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.stickers[r.PathValue("guild")][r.PathValue("sticker")]; !ok {
		notFound(w, "Sticker", 10060)
		return
	}
	delete(s.stickers[r.PathValue("guild")], r.PathValue("sticker"))
	delete(s.stickerFiles, r.PathValue("sticker"))
	w.WriteHeader(http.StatusNoContent)
}

// SoundboardSoundData returns the sound data URI a soundboard sound was
// created with.
func (s *Server) SoundboardSoundData(soundID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.soundData[soundID]
	return data, ok
}

func (s *Server) listSoundboardSounds(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.guild(w, r); !ok {
		return
	}
	items := slices.SortedFunc(maps.Values(s.sounds[r.PathValue("guild")]), func(a, b *discord.SoundboardSound) int {
		return strings.Compare(a.SoundID, b.SoundID)
	})
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getSoundboardSound(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sound, ok := s.sounds[r.PathValue("guild")][r.PathValue("sound")]
	if !ok {
		notFound(w, "Sound", 10097)
		return
	}
	writeJSON(w, http.StatusOK, sound)
}

func (s *Server) createSoundboardSound(w http.ResponseWriter, r *http.Request) {
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
	var data string
	set(body, "sound", &data)
	if !strings.HasPrefix(data, "data:audio/") {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	sound := &discord.SoundboardSound{SoundID: s.newID(), Volume: 1, Available: true, GuildID: g.ID}
	set(body, "name", &sound.Name)
	if !s.setSoundFields(w, body, sound) {
		return
	}
	s.sounds[g.ID][sound.SoundID] = sound
	s.soundData[sound.SoundID] = data
	writeJSON(w, http.StatusOK, sound)
}

func (s *Server) modifySoundboardSound(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sound, ok := s.sounds[r.PathValue("guild")][r.PathValue("sound")]
	if !ok {
		notFound(w, "Sound", 10097)
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	if _, ok := body["sound"]; ok {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	updated := *sound
	set(body, "name", &updated.Name)
	if !s.setSoundFields(w, body, &updated) {
		return
	}
	*sound = updated
	writeJSON(w, http.StatusOK, sound)
}

// setSoundFields applies the volume and emoji of a soundboard sound. A null
// volume means the default of 1, and a sound has at most one emoji.
func (s *Server) setSoundFields(w http.ResponseWriter, body map[string]json.RawMessage, sound *discord.SoundboardSound) bool {
	if _, ok := body["volume"]; ok {
		var volume *float64
		set(body, "volume", &volume)
		sound.Volume = 1
		if volume != nil {
			sound.Volume = *volume
		}
	}
	set(body, "emoji_id", &sound.EmojiID)
	set(body, "emoji_name", &sound.EmojiName)
	if sound.Name == "" || sound.Volume < 0 || sound.Volume > 1 || (sound.EmojiID != nil && sound.EmojiName != nil) {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return false
	}
	return true
}

func (s *Server) deleteSoundboardSound(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sounds[r.PathValue("guild")][r.PathValue("sound")]; !ok {
		notFound(w, "Sound", 10097)
		return
	}
	delete(s.sounds[r.PathValue("guild")], r.PathValue("sound"))
	delete(s.soundData, r.PathValue("sound"))
	w.WriteHeader(http.StatusNoContent)
}

var autoArchiveDurations = []int64{60, 1440, 4320, 10080}

func (s *Server) newThread(id string, parent *discord.Channel, threadType int) *discord.Thread {
	return &discord.Thread{
		ID:               id,
		Type:             threadType,
		GuildID:          parent.GuildID,
		ParentID:         &parent.ID,
		OwnerID:          s.botUserID,
		RateLimitPerUser: parent.DefaultThreadRateLimitPerUser,
		AppliedTags:      []string{},
		ThreadMetadata:   &discord.ThreadMetadata{AutoArchiveDuration: parent.DefaultAutoArchiveDuration},
	}
}

// applyThreadCreate sets the parameters every Start Thread endpoint accepts.
func applyThreadCreate(t *discord.Thread, body map[string]json.RawMessage) error {
	set(body, "name", &t.Name)
	if t.Name == "" || len(t.Name) > 100 {
		return errors.New("name must be 1-100 characters")
	}
	set(body, "auto_archive_duration", &t.ThreadMetadata.AutoArchiveDuration)
	if !slices.Contains(autoArchiveDurations, t.ThreadMetadata.AutoArchiveDuration) {
		return fmt.Errorf("invalid auto_archive_duration %d", t.ThreadMetadata.AutoArchiveDuration)
	}
	set(body, "rate_limit_per_user", &t.RateLimitPerUser)
	return nil
}

// setAppliedTags checks applied_tags against the parent's tags, as Discord
// does for forum and media posts.
func setAppliedTags(t *discord.Thread, parent *discord.Channel, body map[string]json.RawMessage) error {
	if _, ok := body["applied_tags"]; !ok {
		return nil
	}
	if parent.Type != discord.ChannelTypeForum && parent.Type != discord.ChannelTypeMedia {
		return errors.New("applied_tags is only valid in forum and media channels")
	}
	var tags []string
	set(body, "applied_tags", &tags)
	if len(tags) > 5 {
		return errors.New("at most 5 applied_tags")
	}
	for _, id := range tags {
		if !slices.ContainsFunc(parent.AvailableTags, func(tag discord.ForumTag) bool { return tag.ID == id }) {
			return fmt.Errorf("unknown tag %s", id)
		}
	}
	if tags == nil {
		tags = []string{}
	}
	t.AppliedTags = tags
	return nil
}

// startThread models Start Thread without Message, whose type defaults to a
// private thread, and Start Thread in Forum or Media Channel, which posts the
// starter message with the thread's ID.
func (s *Server) startThread(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent, ok := s.channel(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	forum := parent.Type == discord.ChannelTypeForum || parent.Type == discord.ChannelTypeMedia
	threadType := discord.ChannelTypePrivateThread
	if forum {
		threadType = discord.ChannelTypePublicThread
	} else {
		set(body, "type", &threadType)
	}
	switch {
	case forum:
	case parent.Type == discord.ChannelTypeText && (threadType == discord.ChannelTypePublicThread || threadType == discord.ChannelTypePrivateThread):
	case parent.Type == discord.ChannelTypeAnnouncement && threadType == discord.ChannelTypeAnnouncementThread:
	default:
		writeError(w, http.StatusBadRequest, 50024, "Cannot execute action on this channel type")
		return
	}
	t := s.newThread(s.newID(), parent, threadType)
	if err := applyThreadCreate(t, body); err != nil {
		writeError(w, http.StatusBadRequest, 50035, err.Error())
		return
	}
	if threadType == discord.ChannelTypePrivateThread {
		invitable := true
		set(body, "invitable", &invitable)
		t.ThreadMetadata.Invitable = &invitable
	}
	if !forum {
		s.threads[t.ID] = t
		writeJSON(w, http.StatusCreated, t)
		return
	}
	if err := setAppliedTags(t, parent, body); err != nil {
		writeError(w, http.StatusBadRequest, 50035, err.Error())
		return
	}
	if parent.Flags&discord.ChannelFlagRequireTag != 0 && len(t.AppliedTags) == 0 {
		writeError(w, http.StatusBadRequest, 40067, "A tag is required to create a forum post in this channel")
		return
	}
	var msgBody map[string]json.RawMessage
	set(body, "message", &msgBody)
	m := &discord.Message{ID: t.ID, ChannelID: t.ID, Author: &discord.User{ID: s.botUserID, Username: "bot", Bot: true}, Embeds: []discord.Embed{}}
	applyMessage(m, msgBody)
	if m.Content == "" && len(m.Embeds) == 0 {
		writeError(w, http.StatusBadRequest, 50006, "Cannot send an empty message")
		return
	}
	s.threads[t.ID] = t
	s.messages[m.ID] = m
	writeJSON(w, http.StatusCreated, struct {
		*discord.Thread
		Message *discord.Message `json:"message"`
	}{t, m})
}

func (s *Server) startThreadFromMessage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent, ok := s.channel(w, r)
	if !ok {
		return
	}
	m, ok := s.message(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	var threadType int
	switch parent.Type {
	case discord.ChannelTypeText:
		threadType = discord.ChannelTypePublicThread
	case discord.ChannelTypeAnnouncement:
		threadType = discord.ChannelTypeAnnouncementThread
	default:
		writeError(w, http.StatusBadRequest, 50024, "Cannot execute action on this channel type")
		return
	}
	if _, ok := s.threads[m.ID]; ok {
		writeError(w, http.StatusBadRequest, 160004, "A thread has already been created for this message")
		return
	}
	t := s.newThread(m.ID, parent, threadType)
	if err := applyThreadCreate(t, body); err != nil {
		writeError(w, http.StatusBadRequest, 50035, err.Error())
		return
	}
	s.threads[t.ID] = t
	writeJSON(w, http.StatusCreated, t)
}

// modifyThread models Modify Channel for threads: an archived thread only
// accepts requests that unarchive it, and archiving clears PINNED.
func (s *Server) modifyThread(w http.ResponseWriter, r *http.Request, t *discord.Thread) {
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	updated := *t
	meta := *t.ThreadMetadata
	updated.ThreadMetadata = &meta
	archived := meta.Archived
	set(body, "archived", &archived)
	if meta.Archived && archived {
		writeError(w, http.StatusBadRequest, 50083, "Thread is archived")
		return
	}
	parent := s.channels[*t.ParentID]
	forum := parent.Type == discord.ChannelTypeForum || parent.Type == discord.ChannelTypeMedia
	set(body, "name", &updated.Name)
	if updated.Name == "" || len(updated.Name) > 100 {
		writeError(w, http.StatusBadRequest, 50035, "name must be 1-100 characters")
		return
	}
	set(body, "locked", &meta.Locked)
	set(body, "rate_limit_per_user", &updated.RateLimitPerUser)
	set(body, "auto_archive_duration", &meta.AutoArchiveDuration)
	if !slices.Contains(autoArchiveDurations, meta.AutoArchiveDuration) {
		writeError(w, http.StatusBadRequest, 50035, "invalid auto_archive_duration")
		return
	}
	if _, ok := body["invitable"]; ok && t.Type == discord.ChannelTypePrivateThread {
		var invitable bool
		set(body, "invitable", &invitable)
		meta.Invitable = &invitable
	}
	set(body, "flags", &updated.Flags)
	if updated.Flags&discord.ChannelFlagPinned != 0 && !forum {
		writeError(w, http.StatusBadRequest, 50035, "PINNED can only be set on forum and media posts")
		return
	}
	if err := setAppliedTags(&updated, parent, body); err != nil {
		writeError(w, http.StatusBadRequest, 50035, err.Error())
		return
	}
	meta.Archived = archived
	if archived {
		updated.Flags &^= discord.ChannelFlagPinned
	}
	*t = updated
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) deleteThread(id string) {
	delete(s.threads, id)
	for msgID, m := range s.messages {
		if m.ChannelID == id {
			delete(s.messages, msgID)
		}
	}
}
