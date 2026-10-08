package discordtest

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// timestampLayout is how Discord formats scheduled event times, which differs
// from the RFC 3339 "Z" form most configurations use.
const timestampLayout = "2006-01-02T15:04:05-07:00"

func parseTimestamp(raw string) (string, time.Time, error) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", time.Time{}, err
	}
	return t.UTC().Format(timestampLayout), t, nil
}

// validTransition reports whether Discord allows moving an event from one
// status to another.
func validTransition(from, to int64) bool {
	allowed := map[int64][]int64{
		discord.ScheduledEventStatusScheduled: {
			discord.ScheduledEventStatusScheduled, discord.ScheduledEventStatusActive, discord.ScheduledEventStatusCanceled,
		},
		discord.ScheduledEventStatusActive: {discord.ScheduledEventStatusActive, discord.ScheduledEventStatusCompleted},
	}
	return slices.Contains(allowed[from], to)
}

// decodeRecurrenceRule rejects the fields Discord does not let applications
// set.
func decodeRecurrenceRule(raw json.RawMessage) (*discord.RecurrenceRule, error) {
	var in struct {
		discord.RecurrenceRule
		Interval  *int64          `json:"interval"`
		End       json.RawMessage `json:"end"`
		Count     json.RawMessage `json:"count"`
		ByYearDay json.RawMessage `json:"by_year_day"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	for _, f := range []json.RawMessage{in.End, in.Count, in.ByYearDay} {
		if f != nil && string(f) != "null" {
			return nil, errors.New("end, count and by_year_day cannot be set")
		}
	}
	rule := in.RecurrenceRule
	start, _, err := parseTimestamp(rule.Start)
	if err != nil {
		return nil, errors.New("recurrence_rule.start must be an ISO8601 timestamp")
	}
	rule.Start = start
	rule.Interval = 1
	if in.Interval != nil {
		rule.Interval = *in.Interval
	}
	if rule.Frequency < 0 || rule.Frequency > 3 || rule.Interval < 1 || rule.Interval > 2 {
		return nil, errors.New("invalid recurrence frequency or interval")
	}
	return &rule, nil
}

// applyScheduledEvent sets the fields of a create or modify request on e and
// checks the result against the documented requirements for its entity type.
func (s *Server) applyScheduledEvent(e *discord.ScheduledEvent, body map[string]json.RawMessage, creating bool) error {
	wasExternal := e.EntityType == discord.ScheduledEventEntityExternal
	set(body, "entity_type", &e.EntityType)
	if !creating && !wasExternal && e.EntityType == discord.ScheduledEventEntityExternal {
		// Changing to an external event must send these, even unchanged.
		for _, k := range []string{"channel_id", "entity_metadata", "scheduled_end_time"} {
			if _, ok := body[k]; !ok {
				return errors.New(k + " is required when changing entity_type to EXTERNAL")
			}
		}
	}
	set(body, "name", &e.Name)
	set(body, "description", &e.Description)
	set(body, "privacy_level", &e.PrivacyLevel)
	set(body, "channel_id", &e.ChannelID)
	set(body, "scheduled_start_time", &e.ScheduledStartTime)
	set(body, "scheduled_end_time", &e.ScheduledEndTime)
	if e.EntityType == discord.ScheduledEventEntityExternal {
		set(body, "entity_metadata", &e.EntityMetadata)
	} else {
		// Discord silently discards entity_metadata for other entity types.
		e.EntityMetadata = nil
	}
	if raw, ok := body["recurrence_rule"]; ok {
		e.RecurrenceRule = nil
		if string(raw) != "null" {
			rule, err := decodeRecurrenceRule(raw)
			if err != nil {
				return err
			}
			e.RecurrenceRule = rule
		}
	}
	if _, ok := body["image"]; ok {
		var image *string
		set(body, "image", &image)
		if image != nil {
			h := "cover" + s.newID()
			image = &h
		}
		e.Image = image
	}
	if _, ok := body["status"]; ok {
		status := e.Status
		set(body, "status", &status)
		if !validTransition(e.Status, status) {
			return errors.New("invalid status transition")
		}
		e.Status = status
	}

	if n := utf8.RuneCountInString(e.Name); n < 1 || n > 100 {
		return errors.New("name must be 1-100 characters")
	}
	if e.Description != nil && utf8.RuneCountInString(*e.Description) > 1000 {
		return errors.New("description must be at most 1000 characters")
	}
	if e.PrivacyLevel != discord.PrivacyLevelGuildOnly {
		return errors.New("privacy_level must be GUILD_ONLY")
	}
	start, startTime, err := parseTimestamp(e.ScheduledStartTime)
	if err != nil {
		return errors.New("scheduled_start_time must be an ISO8601 timestamp")
	}
	e.ScheduledStartTime = start
	if creating && !startTime.After(time.Now()) {
		return errors.New("scheduled_start_time must be in the future")
	}
	if e.ScheduledEndTime != nil {
		end, endTime, err := parseTimestamp(*e.ScheduledEndTime)
		if err != nil {
			return errors.New("scheduled_end_time must be an ISO8601 timestamp")
		}
		if !endTime.After(startTime) {
			return errors.New("scheduled_end_time must be after scheduled_start_time")
		}
		e.ScheduledEndTime = &end
	}

	switch e.EntityType {
	case discord.ScheduledEventEntityStageInstance, discord.ScheduledEventEntityVoice:
		want := discord.ChannelTypeStage
		if e.EntityType == discord.ScheduledEventEntityVoice {
			want = discord.ChannelTypeVoice
		}
		if e.ChannelID == nil {
			return errors.New("channel_id is required")
		}
		ch, ok := s.channels[*e.ChannelID]
		if !ok || ch.GuildID != e.GuildID || ch.Type != want {
			return errors.New("channel_id must be a channel of the event's entity type")
		}
	case discord.ScheduledEventEntityExternal:
		if e.ChannelID != nil {
			return errors.New("channel_id must be null for external events")
		}
		if e.EntityMetadata == nil || e.EntityMetadata.Location == nil ||
			utf8.RuneCountInString(*e.EntityMetadata.Location) < 1 || utf8.RuneCountInString(*e.EntityMetadata.Location) > 100 {
			return errors.New("entity_metadata.location must be 1-100 characters")
		}
		if e.ScheduledEndTime == nil {
			return errors.New("scheduled_end_time is required for external events")
		}
	default:
		return errors.New("invalid entity_type")
	}
	return nil
}

func (s *Server) scheduledEvent(w http.ResponseWriter, r *http.Request) (*discord.ScheduledEvent, bool) {
	e, ok := s.events[r.PathValue("event")]
	if !ok || e.GuildID != r.PathValue("guild") {
		notFound(w, "Guild Scheduled Event", 10070)
		return nil, false
	}
	return e, true
}

func (s *Server) getScheduledEvent(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.scheduledEvent(w, r); ok {
		writeJSON(w, http.StatusOK, e)
	}
}

func (s *Server) createScheduledEvent(w http.ResponseWriter, r *http.Request) {
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
	if _, ok := body["status"]; ok {
		writeError(w, http.StatusBadRequest, 50035, "status cannot be set on create")
		return
	}
	creator := s.botUserID
	e := &discord.ScheduledEvent{
		ID: s.newID(), GuildID: r.PathValue("guild"), CreatorID: &creator, Status: discord.ScheduledEventStatusScheduled,
	}
	if err := s.applyScheduledEvent(e, body, true); err != nil {
		writeError(w, http.StatusBadRequest, 50035, err.Error())
		return
	}
	s.events[e.ID] = e
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) modifyScheduledEvent(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.scheduledEvent(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	updated := *e
	if err := s.applyScheduledEvent(&updated, body, false); err != nil {
		writeError(w, http.StatusBadRequest, 50035, err.Error())
		return
	}
	*e = updated
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) deleteScheduledEvent(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.scheduledEvent(w, r); ok {
		delete(s.events, e.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

func validTopic(topic string) bool {
	n := utf8.RuneCountInString(topic)
	return n >= 1 && n <= 120
}

func (s *Server) stageInstance(w http.ResponseWriter, r *http.Request) (*discord.StageInstance, bool) {
	si, ok := s.stages[r.PathValue("channel")]
	if !ok {
		notFound(w, "Stage Instance", 10067)
	}
	return si, ok
}

func (s *Server) getStageInstance(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if si, ok := s.stageInstance(w, r); ok {
		writeJSON(w, http.StatusOK, si)
	}
}

func (s *Server) createStageInstance(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	si := &discord.StageInstance{ID: s.newID(), PrivacyLevel: discord.PrivacyLevelGuildOnly}
	set(body, "channel_id", &si.ChannelID)
	set(body, "topic", &si.Topic)
	set(body, "privacy_level", &si.PrivacyLevel)
	set(body, "guild_scheduled_event_id", &si.GuildScheduledEventID)
	ch, ok := s.channels[si.ChannelID]
	if !ok {
		notFound(w, "Channel", 10003)
		return
	}
	if ch.Type != discord.ChannelTypeStage {
		writeError(w, http.StatusBadRequest, 50024, "Cannot execute action on this channel type")
		return
	}
	if _, ok := s.stages[ch.ID]; ok {
		writeError(w, http.StatusBadRequest, 150006, "The Stage is already open")
		return
	}
	if !validTopic(si.Topic) || si.PrivacyLevel < 1 || si.PrivacyLevel > 2 {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	if id := si.GuildScheduledEventID; id != nil {
		if e, ok := s.events[*id]; !ok || e.GuildID != ch.GuildID {
			notFound(w, "Guild Scheduled Event", 10070)
			return
		}
	}
	si.GuildID = ch.GuildID
	s.stages[ch.ID] = si
	writeJSON(w, http.StatusOK, si)
}

func (s *Server) modifyStageInstance(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	si, ok := s.stageInstance(w, r)
	if !ok {
		return
	}
	body, err := decode(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, 50109, err.Error())
		return
	}
	updated := *si
	set(body, "topic", &updated.Topic)
	set(body, "privacy_level", &updated.PrivacyLevel)
	if !validTopic(updated.Topic) || updated.PrivacyLevel < 1 || updated.PrivacyLevel > 2 {
		writeError(w, http.StatusBadRequest, 50035, "Invalid Form Body")
		return
	}
	*si = updated
	writeJSON(w, http.StatusOK, si)
}

func (s *Server) deleteStageInstance(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if si, ok := s.stageInstance(w, r); ok {
		delete(s.stages, si.ChannelID)
		w.WriteHeader(http.StatusNoContent)
	}
}
