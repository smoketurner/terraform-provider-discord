package discordtest

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// template returns the guild's template named in the path.
func (s *Server) template(w http.ResponseWriter, r *http.Request) (*discord.GuildTemplate, bool) {
	t, ok := s.ops.templates[r.PathValue("code")]
	if !ok || t.SourceGuildID != r.PathValue("guild") {
		notFound(w, "Guild Template", 10057)
		return nil, false
	}
	return t, true
}

// applyTemplate validates and applies a template's name and description.
func applyTemplate(t *discord.GuildTemplate, body map[string]json.RawMessage) string {
	set(body, "name", &t.Name)
	set(body, "description", &t.Description)
	if n := utf8.RuneCountInString(t.Name); n < 1 || n > 100 {
		return "name must be between 1 and 100 in length"
	}
	if t.Description != nil && utf8.RuneCountInString(*t.Description) > 120 {
		return "description must be 120 or fewer in length"
	}
	return ""
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guild(w, r)
	if !ok {
		return
	}
	out := []*discord.GuildTemplate{}
	for _, t := range s.ops.templates {
		if t.SourceGuildID == g.ID {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b *discord.GuildTemplate) int { return strings.Compare(a.Code, b.Code) })
	writeJSON(w, http.StatusOK, out)
}

// createTemplate allows one template per guild, as Discord does.
func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
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
	for _, t := range s.ops.templates {
		if t.SourceGuildID == g.ID {
			writeError(w, http.StatusBadRequest, 30031, "Guild already has a template")
			return
		}
	}
	const now = "2024-01-01T00:00:00+00:00"
	t := &discord.GuildTemplate{Code: "tmpl" + s.newID(), CreatorID: s.botUserID, CreatedAt: now, UpdatedAt: now, SourceGuildID: g.ID}
	if msg := applyTemplate(t, body); msg != "" {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: "+msg)
		return
	}
	s.ops.templates[t.Code] = t
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) modifyTemplate(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.template(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	t := *cur
	if msg := applyTemplate(&t, body); msg != "" {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: "+msg)
		return
	}
	*cur = t
	writeJSON(w, http.StatusOK, cur)
}

func (s *Server) syncTemplate(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.template(w, r)
	if !ok {
		return
	}
	t.IsDirty = nil
	t.UpdatedAt = s.now().UTC().Format(timestampLayout)
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.template(w, r)
	if !ok {
		return
	}
	delete(s.ops.templates, t.Code)
	writeJSON(w, http.StatusOK, t)
}

// MarkTemplateDirty records that the guild changed since the template was
// last synced.
func (s *Server) MarkTemplateDirty(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops.templates[code].IsDirty = new(true)
}
