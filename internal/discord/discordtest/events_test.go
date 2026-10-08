package discordtest

import (
	"testing"
	"time"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

func TestScheduledEventStatusTransitions(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	ctx := t.Context()
	create := func() string {
		t.Helper()
		e, err := c.CreateScheduledEvent(ctx, GuildID, discord.Payload{
			"name": "event", "privacy_level": discord.PrivacyLevelGuildOnly, "entity_type": discord.ScheduledEventEntityExternal,
			"entity_metadata":      discord.Payload{"location": "Online"},
			"scheduled_start_time": time.Now().Add(time.Hour).Format(time.RFC3339),
			"scheduled_end_time":   time.Now().Add(2 * time.Hour).Format(time.RFC3339),
		})
		if err != nil {
			t.Fatal(err)
		}
		return e.ID
	}
	status := func(id string, to int) error {
		_, err := c.ModifyScheduledEvent(ctx, GuildID, id, discord.Payload{"status": to})
		return err
	}

	a := create()
	for _, to := range []int{discord.ScheduledEventStatusCompleted, discord.ScheduledEventStatusScheduled + 4} {
		if err := status(a, to); err == nil {
			t.Errorf("scheduled -> %d succeeded", to)
		}
	}
	for _, to := range []int{discord.ScheduledEventStatusActive, discord.ScheduledEventStatusCompleted} {
		if err := status(a, to); err != nil {
			t.Fatalf("moving to %d: %v", to, err)
		}
	}
	if err := status(a, discord.ScheduledEventStatusActive); err == nil {
		t.Error("a completed event was restarted")
	}

	b := create()
	if err := status(b, discord.ScheduledEventStatusCanceled); err != nil {
		t.Fatal(err)
	}
	if err := status(b, discord.ScheduledEventStatusScheduled); err == nil {
		t.Error("a canceled event was rescheduled")
	}
}

func TestScheduledEventCreateErrors(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	for name, p := range map[string]discord.Payload{
		"past start":       {"scheduled_start_time": time.Now().Add(-time.Hour).Format(time.RFC3339)},
		"end before start": {"scheduled_end_time": time.Now().Format(time.RFC3339)},
		"status":           {"status": discord.ScheduledEventStatusActive},
		"recurrence count": {"recurrence_rule": discord.Payload{"start": future, "frequency": 2, "count": 3}},
		"no location":      {"entity_metadata": nil},
	} {
		body := discord.Payload{
			"name": "event", "privacy_level": discord.PrivacyLevelGuildOnly, "entity_type": discord.ScheduledEventEntityExternal,
			"entity_metadata":      discord.Payload{"location": "Online"},
			"scheduled_start_time": future,
			"scheduled_end_time":   time.Now().Add(2 * time.Hour).Format(time.RFC3339),
		}
		for k, v := range p {
			body[k] = v
		}
		if _, err := c.CreateScheduledEvent(t.Context(), GuildID, body); err == nil {
			t.Errorf("%s: create succeeded", name)
		}
	}
}

func TestStageInstanceClosedWithChannel(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	ctx := t.Context()
	ch, err := c.CreateChannel(ctx, GuildID, discord.Payload{"name": "stage", "type": discord.ChannelTypeStage})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateStageInstance(ctx, discord.Payload{"channel_id": ch.ID, "topic": "Live"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetStageInstance(ctx, ch.ID); !discord.IsNotFound(err) {
		t.Errorf("stage instance of a deleted channel: %v", err)
	}
}

func TestScheduledEventWithinFiveYears(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	tooFar := time.Now().AddDate(5, 0, 1)
	for name, times := range map[string][2]time.Time{
		"start": {tooFar, tooFar.Add(time.Hour)},
		"end":   {time.Now().Add(time.Hour), tooFar},
	} {
		_, err := c.CreateScheduledEvent(t.Context(), GuildID, discord.Payload{
			"name": "event", "privacy_level": discord.PrivacyLevelGuildOnly, "entity_type": discord.ScheduledEventEntityExternal,
			"entity_metadata":      discord.Payload{"location": "Online"},
			"scheduled_start_time": times[0].Format(time.RFC3339),
			"scheduled_end_time":   times[1].Format(time.RFC3339),
		})
		if err == nil {
			t.Errorf("%s more than five years ahead: created", name)
		}
	}
}
