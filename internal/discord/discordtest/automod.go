package discordtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// AutoMod trigger types and their per-server limits.
const (
	automodKeyword       = 1
	automodSpam          = 3
	automodKeywordPreset = 4
	automodMentionSpam   = 5
	automodMemberProfile = 6
)

var automodMaxRules = map[int64]int{
	automodKeyword:       6,
	automodSpam:          1,
	automodKeywordPreset: 1,
	automodMentionSpam:   1,
	automodMemberProfile: 1,
}

const automodActionTimeout = 3

func (s *Server) automodRule(w http.ResponseWriter, r *http.Request) (*discord.AutoModerationRule, bool) {
	rule, ok := s.automod[r.PathValue("guild")][r.PathValue("rule")]
	if !ok {
		notFound(w, "Auto Moderation Rule", 0)
	}
	return rule, ok
}

// automodJSON renders a rule the way Discord does: trigger_metadata and
// action metadata carry only the fields of their type, with empty lists
// rather than omitted ones.
func automodJSON(rule *discord.AutoModerationRule) map[string]any {
	m := rule.TriggerMetadata
	nonNil := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	var meta map[string]any
	switch rule.TriggerType {
	case automodKeyword, automodMemberProfile:
		meta = map[string]any{"keyword_filter": nonNil(m.KeywordFilter), "regex_patterns": nonNil(m.RegexPatterns), "allow_list": nonNil(m.AllowList)}
	case automodKeywordPreset:
		presets := m.Presets
		if presets == nil {
			presets = []int64{}
		}
		meta = map[string]any{"presets": presets, "allow_list": nonNil(m.AllowList)}
	case automodMentionSpam:
		var limit int64
		if m.MentionTotalLimit != nil {
			limit = *m.MentionTotalLimit
		}
		meta = map[string]any{"mention_total_limit": limit, "mention_raid_protection_enabled": m.MentionRaidProtectionEnabled != nil && *m.MentionRaidProtectionEnabled}
	default:
		meta = map[string]any{}
	}
	actions := make([]map[string]any, 0, len(rule.Actions))
	for _, a := range rule.Actions {
		am := map[string]any{}
		if md := a.Metadata; md != nil {
			switch a.Type {
			case 1:
				if md.CustomMessage != nil {
					am["custom_message"] = *md.CustomMessage
				}
			case 2:
				am["channel_id"] = md.ChannelID
			case automodActionTimeout:
				am["duration_seconds"] = md.DurationSeconds
			}
		}
		actions = append(actions, map[string]any{"type": a.Type, "metadata": am})
	}
	return map[string]any{
		"id":               rule.ID,
		"guild_id":         rule.GuildID,
		"name":             rule.Name,
		"creator_id":       rule.CreatorID,
		"event_type":       rule.EventType,
		"trigger_type":     rule.TriggerType,
		"trigger_metadata": meta,
		"actions":          actions,
		"enabled":          rule.Enabled,
		"exempt_roles":     nonNil(rule.ExemptRoles),
		"exempt_channels":  nonNil(rule.ExemptChannels),
	}
}

// applyAutomodRule sets the fields Create and Modify Auto Moderation Rule
// accept and checks the documented limits.
func (s *Server) applyAutomodRule(rule *discord.AutoModerationRule, body map[string]json.RawMessage) error {
	set(body, "name", &rule.Name)
	if rule.Name == "" || len(rule.Name) > 100 {
		return errors.New("name must be 1-100 characters")
	}
	set(body, "event_type", &rule.EventType)
	if rule.EventType != 1 && rule.EventType != 2 {
		return fmt.Errorf("invalid event_type %d", rule.EventType)
	}
	if _, ok := body["trigger_metadata"]; ok {
		rule.TriggerMetadata = discord.AutoModerationTriggerMetadata{}
		set(body, "trigger_metadata", &rule.TriggerMetadata)
	}
	m := rule.TriggerMetadata
	if len(m.KeywordFilter) > 1000 || len(m.RegexPatterns) > 10 || len(m.AllowList) > 1000 {
		return errors.New("too many trigger_metadata entries")
	}
	if m.MentionTotalLimit != nil && (*m.MentionTotalLimit < 0 || *m.MentionTotalLimit > 50) {
		return errors.New("mention_total_limit must be at most 50")
	}
	set(body, "actions", &rule.Actions)
	if len(rule.Actions) == 0 {
		return errors.New("actions is required")
	}
	for _, a := range rule.Actions {
		if a.Type < 1 || a.Type > 4 {
			return fmt.Errorf("invalid action type %d", a.Type)
		}
		if a.Type == 2 && (a.Metadata == nil || s.channels[a.Metadata.ChannelID] == nil) {
			return errors.New("send alert message action needs an existing channel_id")
		}
		if a.Type == automodActionTimeout {
			if rule.TriggerType != automodKeyword && rule.TriggerType != automodMentionSpam {
				return errors.New("timeout actions are only allowed for keyword and mention spam rules")
			}
			if a.Metadata == nil || a.Metadata.DurationSeconds == nil || *a.Metadata.DurationSeconds > 2419200 {
				return errors.New("timeout action needs duration_seconds of at most 2419200")
			}
		}
	}
	set(body, "enabled", &rule.Enabled)
	set(body, "exempt_roles", &rule.ExemptRoles)
	set(body, "exempt_channels", &rule.ExemptChannels)
	if len(rule.ExemptRoles) > 20 || len(rule.ExemptChannels) > 50 {
		return errors.New("too many exempt roles or channels")
	}
	return nil
}

func (s *Server) getAutomodRule(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rule, ok := s.automodRule(w, r); ok {
		writeJSON(w, http.StatusOK, automodJSON(rule))
	}
}

func (s *Server) createAutomodRule(w http.ResponseWriter, r *http.Request) {
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
	rule := &discord.AutoModerationRule{ID: s.newID(), GuildID: g.ID, CreatorID: s.botUserID}
	set(body, "trigger_type", &rule.TriggerType)
	limit, ok := automodMaxRules[rule.TriggerType]
	if !ok {
		writeError(w, http.StatusBadRequest, 50035, fmt.Sprintf("Invalid Form Body: invalid trigger_type %d", rule.TriggerType))
		return
	}
	existing := 0
	for _, other := range s.automod[g.ID] {
		if other.TriggerType == rule.TriggerType {
			existing++
		}
	}
	if existing >= limit {
		writeError(w, http.StatusBadRequest, 50035, fmt.Sprintf("Invalid Form Body: maximum of %d rules of trigger_type %d reached", limit, rule.TriggerType))
		return
	}
	if err := s.applyAutomodRule(rule, body); err != nil {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: "+err.Error())
		return
	}
	if s.automod[g.ID] == nil {
		s.automod[g.ID] = map[string]*discord.AutoModerationRule{}
	}
	s.automod[g.ID][rule.ID] = rule
	writeJSON(w, http.StatusOK, automodJSON(rule))
}

func (s *Server) modifyAutomodRule(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rule, ok := s.automodRule(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	// trigger_type is not a Modify parameter; rejecting it catches a client
	// that tries to change it in place.
	if _, ok := body["trigger_type"]; ok {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: trigger_type cannot be changed")
		return
	}
	updated := *rule
	updated.Actions = slices.Clone(rule.Actions)
	if err := s.applyAutomodRule(&updated, body); err != nil {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body: "+err.Error())
		return
	}
	*rule = updated
	writeJSON(w, http.StatusOK, automodJSON(rule))
}

func (s *Server) deleteAutomodRule(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.automodRule(w, r); !ok {
		return
	}
	delete(s.automod[r.PathValue("guild")], r.PathValue("rule"))
	w.WriteHeader(http.StatusNoContent)
}
