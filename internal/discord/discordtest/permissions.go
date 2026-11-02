package discordtest

import (
	"net/http"
	"strconv"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	allPermissions = func() uint64 {
		var all uint64
		for _, bit := range discord.Permissions {
			all |= bit
		}
		return all
	}()
	permAdministrator = discord.Permissions["ADMINISTRATOR"]
	permViewChannel   = discord.Permissions["VIEW_CHANNEL"]
	permConnect       = discord.Permissions["CONNECT"]
)

func (s *Server) handlePermissions(mux *http.ServeMux) {
	mux.HandleFunc("GET /guilds/{guild}/permissions/member/{user}", s.getGuildMemberPermissions)
	mux.HandleFunc("GET /guilds/{guild}/permissions/role/{role}", s.getGuildRolePermissions)
	mux.HandleFunc("GET /channels/{channel}/permissions/member/{user}", s.getChannelMemberPermissions)
	mux.HandleFunc("GET /channels/{channel}/permissions/role/{role}", s.getChannelRolePermissions)
}

func parseBits(v string) uint64 {
	bits, _ := strconv.ParseUint(v, 10, 64)
	return bits
}

func writePermissions(w http.ResponseWriter, bits uint64) {
	writeJSON(w, http.StatusOK, discord.ComputedPermissions{Permissions: strconv.FormatUint(bits, 10)})
}

// basePermissions combines @everyone with the given roles at the server
// level, as in Discord's compute_base_permissions.
func (s *Server) basePermissions(g *discord.Guild, userID string, roleIDs []string) uint64 {
	if userID != "" && userID == g.OwnerID {
		return allPermissions
	}
	bits := parseBits(s.roles[g.ID][g.ID].Permissions)
	for _, id := range roleIDs {
		if role, ok := s.roles[g.ID][id]; ok {
			bits |= parseBits(role.Permissions)
		}
	}
	if bits&permAdministrator != 0 {
		return allPermissions
	}
	return bits
}

// channelPermissions applies a channel's overwrites to base permissions in
// Discord's order: @everyone, then the roles together, then the member.
func channelPermissions(base uint64, ch *discord.Channel, userID string, roleIDs []string) uint64 {
	if base&permAdministrator != 0 {
		return allPermissions
	}
	overwrites := map[string]discord.Overwrite{}
	for _, o := range ch.PermissionOverwrites {
		overwrites[o.ID] = o
	}
	bits := base
	if o, ok := overwrites[ch.GuildID]; ok {
		bits = bits&^parseBits(o.Deny) | parseBits(o.Allow)
	}
	var allow, deny uint64
	for _, id := range roleIDs {
		if o, ok := overwrites[id]; ok && o.Type == 0 {
			allow |= parseBits(o.Allow)
			deny |= parseBits(o.Deny)
		}
	}
	bits = bits&^deny | allow
	if o, ok := overwrites[userID]; ok && userID != "" && o.Type == 1 {
		bits = bits&^parseBits(o.Deny) | parseBits(o.Allow)
	}
	return bits
}

// implicitMask drops the permissions a member cannot use without
// VIEW_CHANNEL, or without CONNECT in a voice or stage channel. Discord does
// not document exactly which permissions CONNECT masks; the fake keeps only
// VIEW_CHANNEL.
func implicitMask(bits uint64, ch *discord.Channel) uint64 {
	if bits&permAdministrator != 0 {
		return bits
	}
	if bits&permViewChannel == 0 {
		return 0
	}
	if (ch.Type == discord.ChannelTypeVoice || ch.Type == discord.ChannelTypeStage) && bits&permConnect == 0 {
		return permViewChannel
	}
	return bits
}

// permissionChannel resolves the channel whose overwrites apply: the channel
// itself, or a thread's parent.
func (s *Server) permissionChannel(w http.ResponseWriter, r *http.Request) (*discord.Channel, bool) {
	id := r.PathValue("channel")
	if s.denied[id] {
		writeError(w, http.StatusForbidden, 50001, "Missing Access")
		return nil, false
	}
	if t, ok := s.threads[id]; ok && t.ParentID != nil {
		id = *t.ParentID
	}
	ch, ok := s.channels[id]
	if !ok {
		notFound(w, "Channel", 10003)
	}
	return ch, ok
}

func (s *Server) getGuildMemberPermissions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	m, ok := s.member(w, r)
	if !ok {
		return
	}
	writePermissions(w, s.basePermissions(g, m.User.ID, m.Roles))
}

func (s *Server) getGuildRolePermissions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	roleID := r.PathValue("role")
	if _, ok := s.roles[g.ID][roleID]; !ok {
		notFound(w, "Role", 10011)
		return
	}
	writePermissions(w, s.basePermissions(g, "", []string{roleID}))
}

func (s *Server) getChannelMemberPermissions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.permissionChannel(w, r)
	if !ok {
		return
	}
	r.SetPathValue("guild", ch.GuildID)
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	m, ok := s.member(w, r)
	if !ok {
		return
	}
	bits := channelPermissions(s.basePermissions(g, m.User.ID, m.Roles), ch, m.User.ID, m.Roles)
	if r.URL.Query().Get("implicit") != "false" && !m.User.Bot {
		bits = implicitMask(bits, ch)
	}
	writePermissions(w, bits)
}

func (s *Server) getChannelRolePermissions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.permissionChannel(w, r)
	if !ok {
		return
	}
	r.SetPathValue("guild", ch.GuildID)
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	roleID := r.PathValue("role")
	if _, ok := s.roles[g.ID][roleID]; !ok {
		notFound(w, "Role", 10011)
		return
	}
	bits := channelPermissions(s.basePermissions(g, "", []string{roleID}), ch, "", []string{roleID})
	if r.URL.Query().Get("implicit") != "false" {
		bits = implicitMask(bits, ch)
	}
	writePermissions(w, bits)
}
