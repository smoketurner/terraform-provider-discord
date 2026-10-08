package discordtest

import (
	"cmp"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// discordEpoch is the first millisecond of 2015, the origin of snowflake
// timestamps.
const discordEpoch = 1420070400000

// bulkDeleteMaxAge is how old a message can be for Bulk Delete Messages.
const bulkDeleteMaxAge = 14 * 24 * time.Hour

// operations holds the state behind the endpoints that make one-off changes:
// the actions of the provider, server templates and invite target users.
type operations struct {
	templates     map[string]*discord.GuildTemplate
	inviteTargets map[string][]string
	crossposted   map[string]bool
	// endedPolls holds the IDs of poll messages whose poll has ended.
	endedPolls  map[string]bool
	voiceStatus map[string]string
	prunes      []PruneRequest
	// bodies holds the body of every request, in the order of requests.
	bodies [][]byte
}

// PruneRequest records a Begin Guild Prune request.
type PruneRequest struct {
	Days         int64
	IncludeRoles []string
	Removed      []string
}

func (s *Server) handleOperations(mux *http.ServeMux) {
	s.ops = operations{
		templates:     map[string]*discord.GuildTemplate{},
		inviteTargets: map[string][]string{},
		crossposted:   map[string]bool{},
		endedPolls:    map[string]bool{},
		voiceStatus:   map[string]string{},
	}
	mux.HandleFunc("POST /channels/{channel}/messages/{message}/crosspost", s.crosspostMessage)
	mux.HandleFunc("POST /channels/{channel}/messages/bulk-delete", s.bulkDeleteMessages)
	mux.HandleFunc("POST /channels/{channel}/polls/{message}/expire", s.endPoll)
	mux.HandleFunc("PUT /channels/{channel}/voice-status", s.setVoiceStatus)
	mux.HandleFunc("GET /guilds/{guild}/prune", s.prune(false))
	mux.HandleFunc("POST /guilds/{guild}/prune", s.prune(true))
	mux.HandleFunc("POST /guilds/{guild}/bulk-ban", s.bulkBan)
	mux.HandleFunc("GET /guilds/{guild}/templates", s.listTemplates)
	mux.HandleFunc("POST /guilds/{guild}/templates", s.createTemplate)
	mux.HandleFunc("PATCH /guilds/{guild}/templates/{code}", s.modifyTemplate)
	mux.HandleFunc("PUT /guilds/{guild}/templates/{code}", s.syncTemplate)
	mux.HandleFunc("DELETE /guilds/{guild}/templates/{code}", s.deleteTemplate)
	mux.HandleFunc("GET /invites/{code}/target-users", s.getInviteTargets)
	mux.HandleFunc("POST /invites/{code}/target-users/bulk-add", s.changeInviteTargets(true))
	mux.HandleFunc("POST /invites/{code}/target-users/bulk-delete", s.changeInviteTargets(false))
}

// LastRequestBody returns the top-level fields of the JSON body of the last
// request matching "METHOD /path", or nil when there was none.
func (s *Server) LastRequestBody(methodPath string) map[string]json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.requests) - 1; i >= 0; i-- {
		if s.requests[i] == methodPath {
			var body map[string]json.RawMessage
			_ = json.Unmarshal(s.ops.bodies[i], &body)
			return body
		}
	}
	return nil
}

// newSnowflake returns an ID whose timestamp is the fake's current time, as
// Discord's message IDs have.
func (s *Server) newSnowflake() string {
	s.nextID++
	seq := int64(s.nextID & 0x3FFFFF) //nolint:gosec // Masked to 22 bits.
	return strconv.FormatInt((s.now().UnixMilli()-discordEpoch)<<22|seq, 10)
}

func snowflakeTime(id string) (time.Time, bool) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(n>>22 + discordEpoch), true
}

func (s *Server) crosspostMessage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channel(w, r)
	if !ok {
		return
	}
	m, ok := s.message(w, r)
	if !ok {
		return
	}
	if ch.Type != discord.ChannelTypeAnnouncement {
		writeError(w, http.StatusBadRequest, 50068, "Invalid message type")
		return
	}
	if s.ops.crossposted[m.ID] {
		writeError(w, http.StatusBadRequest, 40033, "This message has already been crossposted")
		return
	}
	s.ops.crossposted[m.ID] = true
	writeJSON(w, http.StatusOK, m)
}

// Crossposted reports whether a message has been crossposted.
func (s *Server) Crossposted(messageID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ops.crossposted[messageID]
}

// bulkDeleteMessages counts unknown message IDs toward the 2-100 limit and
// rejects duplicates and messages older than two weeks, as Discord does.
func (s *Server) bulkDeleteMessages(w http.ResponseWriter, r *http.Request) {
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
	var ids []string
	set(body, "messages", &ids)
	if len(ids) < 2 || len(ids) > 100 {
		writeError(w, http.StatusBadRequest, 50016, "Provided too few or too many messages to delete. Must provide at least 2 and fewer than 100 messages to delete.")
		return
	}
	if len(slices.Compact(slices.Sorted(slices.Values(ids)))) != len(ids) {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: duplicate message IDs")
		return
	}
	for _, id := range ids {
		if t, ok := snowflakeTime(id); !ok || s.now().Sub(t) > bulkDeleteMaxAge {
			writeError(w, http.StatusBadRequest, 50034, "A message provided was too old to bulk delete")
			return
		}
	}
	for _, id := range ids {
		if m, ok := s.messages[id]; ok && m.ChannelID == ch.ID {
			delete(s.messages, id)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ChannelMessages returns the contents of a channel's messages, oldest
// first.
func (s *Server) ChannelMessages(channelID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var msgs []*discord.Message
	for _, m := range s.messages {
		if m.ChannelID == channelID {
			msgs = append(msgs, m)
		}
	}
	slices.SortFunc(msgs, func(a, b *discord.Message) int {
		ai, _ := strconv.ParseInt(a.ID, 10, 64)
		bi, _ := strconv.ParseInt(b.ID, 10, 64)
		return cmp.Compare(ai, bi)
	})
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

// AddPoll posts a poll from the bot in a channel and returns the message ID.
func (s *Server) AddPoll(channelID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry := s.now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	m := &discord.Message{
		ID: s.newSnowflake(), ChannelID: channelID, Author: &discord.User{ID: s.botUserID, Username: "bot", Bot: true},
		Embeds: []discord.Embed{}, Attachments: []discord.Attachment{}, Timestamp: s.now().UTC().Format(time.RFC3339Nano),
		Poll: &discord.Poll{
			Question: discord.PollMedia{Text: "poll"}, Expiry: &expiry, LayoutType: 1,
			Answers: []discord.PollAnswer{{AnswerID: 1, PollMedia: discord.PollMedia{Text: "yes"}}},
		},
	}
	s.messages[m.ID] = m
	return m.ID
}

// PollEnded reports whether a poll has ended.
func (s *Server) PollEnded(messageID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ops.endedPolls[messageID]
}

func (s *Server) endPoll(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.channel(w, r); !ok {
		return
	}
	m, ok := s.message(w, r)
	if !ok {
		return
	}
	switch {
	case m.Poll == nil:
		writeError(w, http.StatusBadRequest, 520006, "Cannot expire a non-poll message")
	case s.ops.endedPolls[m.ID]:
		writeError(w, http.StatusBadRequest, 520001, "Poll has already ended")
	default:
		s.ops.endedPolls[m.ID] = true
		writeJSON(w, http.StatusOK, m)
	}
}

func (s *Server) setVoiceStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channel(w, r)
	if !ok {
		return
	}
	if ch.Type != discord.ChannelTypeVoice {
		writeError(w, http.StatusBadRequest, 50024, "Cannot execute action on this channel type")
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	var status *string
	set(body, "status", &status)
	switch {
	case status == nil || *status == "":
		delete(s.ops.voiceStatus, ch.ID)
	case utf8.RuneCountInString(*status) > 500:
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: status must be 500 or fewer in length")
		return
	default:
		s.ops.voiceStatus[ch.ID] = *status
	}
	w.WriteHeader(http.StatusNoContent)
}

// VoiceStatus returns a voice channel's status and whether it has one.
func (s *Server) VoiceStatus(channelID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status, ok := s.ops.voiceStatus[channelID]
	return status, ok
}

// prunable lists the members a prune would remove. The fake does not track
// activity, so every member other than the owner counts as inactive; as on
// Discord, a member is only included when all their roles are included.
func (s *Server) prunable(g *discord.Guild, includeRoles []string) []string {
	var out []string
	for id, m := range s.members[g.ID] {
		if id == g.OwnerID {
			continue
		}
		if !slices.ContainsFunc(m.Roles, func(role string) bool { return !slices.Contains(includeRoles, role) }) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func (s *Server) prune(begin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		g, ok := s.guild(w, r)
		if !ok {
			return
		}
		days, compute := int64(7), true
		var includeRoles []string
		if begin {
			body, err := decode(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, 50109, err.Error())
				return
			}
			set(body, "days", &days)
			set(body, "compute_prune_count", &compute)
			set(body, "include_roles", &includeRoles)
		} else {
			q := r.URL.Query()
			if q.Has("days") {
				days, _ = strconv.ParseInt(q.Get("days"), 10, 64)
			}
			if v := q.Get("include_roles"); v != "" {
				includeRoles = strings.Split(v, ",")
			}
		}
		if days < 1 || days > 30 {
			writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: days must be between 1 and 30")
			return
		}
		for _, role := range includeRoles {
			if _, ok := s.roles[g.ID][role]; !ok {
				writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: unknown role "+role)
				return
			}
		}
		removed := s.prunable(g, includeRoles)
		pruned := new(int64(len(removed)))
		if begin {
			for _, id := range removed {
				delete(s.members[g.ID], id)
			}
			s.ops.prunes = append(s.ops.prunes, PruneRequest{Days: days, IncludeRoles: includeRoles, Removed: removed})
			if !compute {
				pruned = nil
			}
		}
		writeJSON(w, http.StatusOK, discord.PruneResult{Pruned: pruned})
	}
}

// Prunes returns every Begin Guild Prune request so far.
func (s *Server) Prunes() []PruneRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.ops.prunes)
}

// bulkBan bans each user who is not already banned and reports the rest as
// failed. When nobody could be banned the request fails, as on Discord.
func (s *Server) bulkBan(w http.ResponseWriter, r *http.Request) {
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
	var ids []string
	var seconds int
	set(body, "user_ids", &ids)
	set(body, "delete_message_seconds", &seconds)
	if len(ids) == 0 || len(ids) > 200 {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: user_ids must have between 1 and 200 items")
		return
	}
	if seconds < 0 || seconds > maxBanDeleteMessageSeconds {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: delete_message_seconds must be between 0 and 604800")
		return
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
	result := discord.BulkBanResult{BannedUsers: []string{}, FailedUsers: []string{}}
	for _, id := range ids {
		if _, banned := s.bans[g.ID][id]; banned || id == g.OwnerID {
			result.FailedUsers = append(result.FailedUsers, id)
			continue
		}
		user := &discord.User{ID: id, Username: "user" + id, Discriminator: "0"}
		if m, ok := s.members[g.ID][id]; ok {
			user = m.User
		}
		s.bans[g.ID][id] = &discord.Ban{Reason: reason, User: user}
		delete(s.members[g.ID], id)
		result.BannedUsers = append(result.BannedUsers, id)
	}
	if len(result.BannedUsers) == 0 {
		writeError(w, http.StatusBadRequest, 500000, "Failed to ban users")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// applyInviteTargets sets the roles an invite grants and what it targets
// from a Create Channel Invite request.
func (s *Server) applyInviteTargets(guildID string, inv *discord.Invite, body map[string]json.RawMessage) string {
	var roleIDs []string
	set(body, "role_ids", &roleIDs)
	for _, id := range roleIDs {
		role, ok := s.roles[guildID][id]
		if !ok || id == guildID {
			return "unknown role " + id
		}
		inv.Roles = append(inv.Roles, discord.InviteRole{ID: role.ID, Name: role.Name})
	}
	set(body, "target_type", &inv.TargetType)
	var userID, appID string
	set(body, "target_user_id", &userID)
	set(body, "target_application_id", &appID)
	switch inv.TargetType {
	case 0:
		if userID != "" || appID != "" {
			return "target_type is required with a target"
		}
	case discord.InviteTargetStream:
		if userID == "" || appID != "" {
			return "target_user_id is required for stream invites"
		}
		inv.TargetUser = &discord.User{ID: userID, Username: "user" + userID, Discriminator: "0"}
	case discord.InviteTargetEmbeddedApplication:
		if appID == "" || userID != "" {
			return "target_application_id is required for embedded application invites"
		}
		inv.TargetApplication = &discord.InviteApplication{ID: appID}
	default:
		return "invalid target_type"
	}
	var targets []string
	set(body, "target_user_ids", &targets)
	if len(targets) > 1000 {
		return "target_user_ids must have 1000 or fewer items"
	}
	return ""
}

func (s *Server) getInviteTargets(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	code := r.PathValue("code")
	if _, ok := s.invites[code]; !ok {
		notFound(w, "Invite", 10006)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	var b strings.Builder
	b.WriteString("user_id\r\n")
	for _, id := range s.ops.inviteTargets[code] {
		b.WriteString(id + "\r\n")
	}
	_, _ = w.Write([]byte(b.String()))
}

func (s *Server) changeInviteTargets(add bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		code := r.PathValue("code")
		if _, ok := s.invites[code]; !ok {
			notFound(w, "Invite", 10006)
			return
		}
		body, err := decode(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, 50109, err.Error())
			return
		}
		var ids []string
		set(body, "user_ids", &ids)
		if len(ids) == 0 || len(ids) > 1000 {
			writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: user_ids must have between 1 and 1000 items")
			return
		}
		targets := slices.DeleteFunc(slices.Clone(s.ops.inviteTargets[code]), func(id string) bool { return slices.Contains(ids, id) })
		if add {
			targets = append(targets, ids...)
		}
		slices.Sort(targets)
		s.ops.inviteTargets[code] = targets
		w.WriteHeader(http.StatusNoContent)
	}
}

// SetInviteTargetUsers replaces an invite's target users outside of the
// provider's requests.
func (s *Server) SetInviteTargetUsers(code string, userIDs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops.inviteTargets[code] = slices.Sorted(slices.Values(userIDs))
}
