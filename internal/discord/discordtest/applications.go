package discordtest

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// maxRoleConnectionMetadata is how many metadata records an application can
// have.
const maxRoleConnectionMetadata = 5

var (
	roleConnectionKeyRegexp = regexp.MustCompile(`^[a-z0-9_]{1,50}$`)
	emojiNameRegexp         = regexp.MustCompile(`^[A-Za-z0-9_]{2,32}$`)
)

func (s *Server) handleApplications(mux *http.ServeMux) {
	mux.HandleFunc("PATCH /users/@me", s.modifyCurrentUser)
	mux.HandleFunc("PATCH /guilds/{guild}/members/@me", s.modifyCurrentMember)
	mux.HandleFunc("PATCH /applications/@me", s.modifyCurrentApplication)
	mux.HandleFunc("GET /applications/{app}/role-connections/metadata", s.getRoleConnectionMetadata)
	mux.HandleFunc("PUT /applications/{app}/role-connections/metadata", s.updateRoleConnectionMetadata)
	mux.HandleFunc("GET /applications/{app}/emojis/{emoji}", s.getApplicationEmoji)
	mux.HandleFunc("POST /applications/{app}/emojis", s.createApplicationEmoji)
	mux.HandleFunc("PATCH /applications/{app}/emojis/{emoji}", s.modifyApplicationEmoji)
	mux.HandleFunc("DELETE /applications/{app}/emojis/{emoji}", s.deleteApplicationEmoji)
}

func invalidForm(w http.ResponseWriter, msg string) {
	writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: "+msg)
}

func (s *Server) modifyCurrentUser(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	bot := s.application().Bot
	u := *bot
	set(body, "username", &u.Username)
	if n := utf8.RuneCountInString(u.Username); n < 2 || n > 32 {
		invalidForm(w, "username must be between 2 and 32 in length")
		return
	}
	s.setImage(body, "avatar", &u.Avatar)
	s.setImage(body, "banner", &u.Banner)
	*bot = u
	writeJSON(w, http.StatusOK, bot)
}

// botMember returns the bot's membership in the request's guild, adding the
// bot to the guild on first use, or writes 404 when the guild does not exist.
func (s *Server) botMember(w http.ResponseWriter, r *http.Request) (*discord.Member, bool) {
	g, ok := s.guild(w, r)
	if !ok {
		return nil, false
	}
	m, ok := s.members[g.ID][s.botUserID]
	if !ok {
		m = &discord.Member{User: s.application().Bot, Roles: []string{}, JoinedAt: "2024-01-01T00:00:00.000000+00:00"}
		s.members[g.ID][s.botUserID] = m
	}
	return m, true
}

func (s *Server) modifyCurrentMember(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.botMember(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	m := *cur
	set(body, "nick", &m.Nick)
	if m.Nick != nil && *m.Nick == "" {
		m.Nick = nil
	}
	if m.Nick != nil && utf8.RuneCountInString(*m.Nick) > 32 {
		invalidForm(w, "nick must be 32 or fewer in length")
		return
	}
	var bio *string
	if _, ok := body["bio"]; ok {
		set(body, "bio", &bio)
		if bio != nil && utf8.RuneCountInString(*bio) > 300 {
			invalidForm(w, "bio must be 300 or fewer in length")
			return
		}
		if bio == nil || *bio == "" {
			delete(s.botBios, r.PathValue("guild"))
		} else {
			s.botBios[r.PathValue("guild")] = *bio
		}
	}
	s.setImage(body, "avatar", &m.Avatar)
	s.setImage(body, "banner", &m.Banner)
	*cur = m
	writeJSON(w, http.StatusOK, cur)
}

// BotBio returns the bot's server profile bio in a guild, which Discord does
// not return in member objects, or "" when none is set.
func (s *Server) BotBio(guildID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.botBios[guildID]
}

// setApplicationURL sets a URL setting, clearing it on null or "".
func setApplicationURL(body map[string]json.RawMessage, key string, dst **string) bool {
	if _, ok := body[key]; !ok {
		return true
	}
	var v *string
	set(body, key, &v)
	if v == nil || *v == "" {
		*dst = nil
		return true
	}
	*dst = v
	return strings.HasPrefix(*v, "https://") || strings.HasPrefix(*v, "http://")
}

func (s *Server) modifyCurrentApplication(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	cur := s.application()
	a := *cur
	set(body, "description", &a.Description)
	if utf8.RuneCountInString(a.Description) > 400 {
		invalidForm(w, "description must be 400 or fewer in length")
		return
	}
	if _, ok := body["tags"]; ok {
		a.Tags = []string{}
		set(body, "tags", &a.Tags)
		if len(a.Tags) > 5 || slices.ContainsFunc(a.Tags, func(t string) bool { return t == "" || utf8.RuneCountInString(t) > 20 }) {
			invalidForm(w, "tags must be at most 5 tags of 1-20 characters")
			return
		}
	}
	for key, dst := range map[string]**string{
		"custom_install_url":                &a.CustomInstallURL,
		"role_connections_verification_url": &a.RoleConnectionsVerificationURL,
		"interactions_endpoint_url":         &a.InteractionsEndpointURL,
		"event_webhooks_url":                &a.EventWebhooksURL,
	} {
		if !setApplicationURL(body, key, dst) {
			invalidForm(w, key+" must be a URL")
			return
		}
	}
	if _, ok := body["install_params"]; ok {
		set(body, "install_params", &a.InstallParams)
	}
	if _, ok := body["integration_types_config"]; ok {
		set(body, "integration_types_config", &a.IntegrationTypesConfig)
		for k := range a.IntegrationTypesConfig {
			if k != discord.IntegrationTypeGuildInstall && k != discord.IntegrationTypeUserInstall {
				invalidForm(w, "unknown integration type "+k)
				return
			}
		}
		if len(a.IntegrationTypesConfig) == 0 {
			invalidForm(w, "integration_types_config must have at least one integration type")
			return
		}
	}
	if _, ok := body["event_webhooks_status"]; ok {
		set(body, "event_webhooks_status", &a.EventWebhooksStatus)
		if a.EventWebhooksStatus != discord.EventWebhooksDisabled && a.EventWebhooksStatus != discord.EventWebhooksEnabled {
			invalidForm(w, "event_webhooks_status must be 1 or 2")
			return
		}
	}
	if _, ok := body["event_webhooks_types"]; ok {
		a.EventWebhooksTypes = []string{}
		set(body, "event_webhooks_types", &a.EventWebhooksTypes)
	}
	if _, ok := body["flags"]; ok {
		var flags int64
		set(body, "flags", &flags)
		if flags&^discord.ApplicationLimitedIntentFlags != 0 {
			invalidForm(w, "only the limited intent flags can be changed")
			return
		}
		a.Flags = a.Flags&^discord.ApplicationLimitedIntentFlags | flags
	}
	s.setImage(body, "icon", &a.Icon)
	s.setImage(body, "cover_image", &a.CoverImage)
	*cur = a
	writeJSON(w, http.StatusOK, cur)
}

// ownApplication checks that the request is for the bot's own application, the
// only one a bot token can manage.
func (s *Server) ownApplication(w http.ResponseWriter, r *http.Request) bool {
	if r.PathValue("app") != ApplicationID {
		notFound(w, "Application", 10002)
		return false
	}
	return true
}

func (s *Server) getRoleConnectionMetadata(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ownApplication(w, r) {
		writeJSON(w, http.StatusOK, s.roleConnections)
	}
}

func validRoleConnectionMetadata(records []discord.RoleConnectionMetadata) string {
	if len(records) > maxRoleConnectionMetadata {
		return "must be 5 or fewer in length"
	}
	keys := map[string]bool{}
	for _, m := range records {
		switch {
		case m.Type < 1 || m.Type > 8:
			return "unknown metadata type"
		case !roleConnectionKeyRegexp.MatchString(m.Key):
			return "key must be 1-50 characters of a-z, 0-9 and _"
		case keys[m.Key]:
			return "duplicate key " + m.Key
		case m.Name == "" || utf8.RuneCountInString(m.Name) > 100:
			return "name must be between 1 and 100 in length"
		case m.Description == "" || utf8.RuneCountInString(m.Description) > 200:
			return "description must be between 1 and 200 in length"
		}
		keys[m.Key] = true
	}
	return ""
}

func (s *Server) updateRoleConnectionMetadata(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ownApplication(w, r) {
		return
	}
	records := []discord.RoleConnectionMetadata{}
	if err := json.NewDecoder(r.Body).Decode(&records); err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	if msg := validRoleConnectionMetadata(records); msg != "" {
		invalidForm(w, msg)
		return
	}
	if records == nil {
		records = []discord.RoleConnectionMetadata{}
	}
	s.roleConnections = records
	writeJSON(w, http.StatusOK, records)
}

// SetRoleConnectionMetadata replaces the application's role connection
// metadata outside Terraform, as in the Developer Portal.
func (s *Server) SetRoleConnectionMetadata(records []discord.RoleConnectionMetadata) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roleConnections = records
}

func (s *Server) applicationEmoji(w http.ResponseWriter, r *http.Request) (*discord.Emoji, bool) {
	if !s.ownApplication(w, r) {
		return nil, false
	}
	e, ok := s.appEmojis[r.PathValue("emoji")]
	if !ok {
		notFound(w, "Emoji", 10014)
	}
	return e, ok
}

func (s *Server) getApplicationEmoji(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.applicationEmoji(w, r); ok {
		writeJSON(w, http.StatusOK, e)
	}
}

func (s *Server) createApplicationEmoji(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ownApplication(w, r) {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	var name, image string
	set(body, "name", &name)
	set(body, "image", &image)
	if !emojiNameRegexp.MatchString(name) || !strings.HasPrefix(image, "data:image/") {
		invalidForm(w, "name must be 2-32 letters, digits or underscores and image a data URI")
		return
	}
	for _, e := range s.appEmojis {
		if e.Name == name {
			invalidForm(w, "an emoji named "+name+" already exists")
			return
		}
	}
	e := &discord.Emoji{ID: s.newID(), Name: name, Roles: []string{}, Animated: strings.HasPrefix(image, "data:image/gif")}
	s.appEmojis[e.ID] = e
	writeJSON(w, http.StatusCreated, e)
}

func (s *Server) modifyApplicationEmoji(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.applicationEmoji(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	name := e.Name
	set(body, "name", &name)
	if !emojiNameRegexp.MatchString(name) {
		invalidForm(w, "name must be 2-32 letters, digits or underscores")
		return
	}
	e.Name = name
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) deleteApplicationEmoji(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.applicationEmoji(w, r); ok {
		delete(s.appEmojis, r.PathValue("emoji"))
		w.WriteHeader(http.StatusNoContent)
	}
}
