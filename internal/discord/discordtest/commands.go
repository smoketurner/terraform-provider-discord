package discordtest

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// chatInputNameRegexp is the documented pattern for CHAT_INPUT command and
// option names; letters must also be lowercase where a lowercase exists.
var chatInputNameRegexp = regexp.MustCompile(`^[-_\x{02BC}\p{L}\p{N}\p{Devanagari}\p{Thai}]{1,32}$`)

func (s *Server) handleApplicationCommands(mux *http.ServeMux) {
	mux.HandleFunc("POST /applications/{app}/commands", s.createCommand)
	mux.HandleFunc("GET /applications/{app}/commands/{command}", s.getCommand)
	mux.HandleFunc("PATCH /applications/{app}/commands/{command}", s.editCommand)
	mux.HandleFunc("DELETE /applications/{app}/commands/{command}", s.deleteCommand)
	mux.HandleFunc("POST /applications/{app}/guilds/{guild}/commands", s.createCommand)
	mux.HandleFunc("GET /applications/{app}/guilds/{guild}/commands/{command}", s.getCommand)
	mux.HandleFunc("PATCH /applications/{app}/guilds/{guild}/commands/{command}", s.editCommand)
	mux.HandleFunc("DELETE /applications/{app}/guilds/{guild}/commands/{command}", s.deleteCommand)
}

// commandScope checks the application and guild of a command request and
// returns the guild ID, empty for global commands.
func (s *Server) commandScope(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.PathValue("app") != ApplicationID {
		writeError(w, http.StatusForbidden, 20012, "You are not authorized to perform this action on this application")
		return "", false
	}
	guildID := r.PathValue("guild")
	if guildID == "" {
		return "", true
	}
	_, ok := s.guild(w, r)
	return guildID, ok
}

func (s *Server) command(w http.ResponseWriter, r *http.Request) (*discord.ApplicationCommand, bool) {
	guildID, ok := s.commandScope(w, r)
	if !ok {
		return nil, false
	}
	cmd, ok := s.commands[r.PathValue("command")]
	if !ok || cmd.GuildID != guildID {
		notFound(w, "application command", 10063)
		return nil, false
	}
	return cmd, true
}

// conflicting returns the other command of the same type and name in the
// same scope, which Discord does not allow.
func (s *Server) conflicting(cmd *discord.ApplicationCommand) *discord.ApplicationCommand {
	for _, other := range s.commands {
		if other.ID != cmd.ID && other.GuildID == cmd.GuildID && other.Type == cmd.Type && other.Name == cmd.Name {
			return other
		}
	}
	return nil
}

func applyCommand(cmd *discord.ApplicationCommand, body map[string]json.RawMessage) {
	set(body, "name", &cmd.Name)
	set(body, "name_localizations", &cmd.NameLocalizations)
	set(body, "description", &cmd.Description)
	set(body, "description_localizations", &cmd.DescriptionLocalizations)
	set(body, "options", &cmd.Options)
	set(body, "default_member_permissions", &cmd.DefaultMemberPermissions)
	set(body, "nsfw", &cmd.NSFW)
	set(body, "contexts", &cmd.Contexts)
	set(body, "integration_types", &cmd.IntegrationTypes)
	if cmd.IntegrationTypes == nil {
		// The application's default installation context.
		cmd.IntegrationTypes = []int64{0}
	}
}

func validCommandName(name string, chatInput bool) bool {
	if chatInput {
		return chatInputNameRegexp.MatchString(name) && strings.ToLower(name) == name
	}
	n := utf8.RuneCountInString(name)
	return n >= 1 && n <= 32
}

func validDescription(d string) bool {
	n := utf8.RuneCountInString(d)
	return n >= 1 && n <= 100
}

func validCommand(cmd *discord.ApplicationCommand) bool {
	chatInput := cmd.Type == discord.ApplicationCommandTypeChatInput
	if !validCommandName(cmd.Name, chatInput) {
		return false
	}
	if !chatInput {
		return cmd.Description == "" && len(cmd.Options) == 0
	}
	return validDescription(cmd.Description) && validOptions(cmd.Options, 1)
}

// validOptions checks the documented option rules, so tests catch payloads
// that only Discord would reject.
func validOptions(opts []discord.ApplicationCommandOption, depth int) bool {
	if len(opts) > 25 {
		return false
	}
	names := map[string]bool{}
	optional := false
	for _, o := range opts {
		if names[o.Name] || !validCommandName(o.Name, true) || !validDescription(o.Description) {
			return false
		}
		names[o.Name] = true
		if o.Required && optional {
			return false
		}
		optional = optional || !o.Required
		if len(o.Choices) > 25 || (o.Autocomplete && len(o.Choices) > 0) {
			return false
		}
		if !validNesting(o, depth) || !validOptions(o.Options, depth+1) {
			return false
		}
	}
	return true
}

// validNesting allows subcommand groups only at the top level, holding
// subcommands, and subcommands at the top level or in a group, holding
// parameters.
func validNesting(o discord.ApplicationCommandOption, depth int) bool {
	switch o.Type {
	case discord.CommandOptionTypeSubCommandGroup:
		if depth != 1 {
			return false
		}
		for _, sub := range o.Options {
			if sub.Type != discord.CommandOptionTypeSubCommand {
				return false
			}
		}
	case discord.CommandOptionTypeSubCommand:
		if depth > 2 {
			return false
		}
		for _, sub := range o.Options {
			if sub.Type == discord.CommandOptionTypeSubCommand || sub.Type == discord.CommandOptionTypeSubCommandGroup {
				return false
			}
		}
	default:
		return len(o.Options) == 0
	}
	return true
}

func (s *Server) getCommand(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cmd, ok := s.command(w, r); ok {
		writeJSON(w, http.StatusOK, cmd)
	}
}

// createCommand upserts by name, as Discord does: a command of the same type
// and name in the scope is overwritten (200), otherwise one is created (201).
func (s *Server) createCommand(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	guildID, ok := s.commandScope(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	cmd := &discord.ApplicationCommand{
		ID:            s.newID(),
		Type:          discord.ApplicationCommandTypeChatInput,
		ApplicationID: ApplicationID,
		GuildID:       guildID,
		Version:       s.newID(),
	}
	set(body, "type", &cmd.Type)
	applyCommand(cmd, body)
	if !validCommand(cmd) {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	if s.commands == nil {
		s.commands = map[string]*discord.ApplicationCommand{}
	}
	status := http.StatusCreated
	if existing := s.conflicting(cmd); existing != nil {
		cmd.ID = existing.ID
		status = http.StatusOK
	}
	s.commands[cmd.ID] = cmd
	writeJSON(w, status, cmd)
}

func (s *Server) editCommand(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.command(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	updated := *cmd
	applyCommand(&updated, body)
	if !validCommand(&updated) || s.conflicting(&updated) != nil {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	updated.Version = s.newID()
	*cmd = updated
	writeJSON(w, http.StatusOK, cmd)
}

func (s *Server) deleteCommand(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cmd, ok := s.command(w, r); ok {
		delete(s.commands, cmd.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}
