package discordtest

import (
	"slices"
	"testing"
	"time"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

func TestBulkDeleteMessagesLimits(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	ctx := t.Context()
	ch, err := c.CreateChannel(ctx, GuildID, discord.Payload{"name": "bulk", "type": discord.ChannelTypeText})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for range 4 {
		m, err := c.CreateMessage(ctx, ch.ID, discord.Payload{"content": "spam"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if created, ok := snowflakeTime(ids[0]); !ok || time.Since(created) > time.Minute {
		t.Fatalf("message ID %s does not encode the current time", ids[0])
	}

	for name, tc := range map[string]struct {
		ids  []string
		code int
	}{
		"one":       {ids[:1], 50016},
		"duplicate": {[]string{ids[0], ids[0]}, 50035},
		"old":       {[]string{ids[0], "100000000000000000"}, 50034},
	} {
		if err := c.BulkDeleteMessages(ctx, ch.ID, tc.ids); apiErrorCode(err) != tc.code {
			t.Errorf("%s: code %d (%v), want %d", name, apiErrorCode(err), err, tc.code)
		}
	}
	// Unknown IDs count toward the limits and are otherwise ignored.
	if err := c.DeleteMessage(ctx, ch.ID, ids[3]); err != nil {
		t.Fatal(err)
	}
	if err := c.BulkDeleteMessages(ctx, ch.ID, []string{ids[0], ids[1], ids[3]}); err != nil {
		t.Fatal(err)
	}
	if got := s.ChannelMessages(ch.ID); !slices.Equal(got, []string{"spam"}) {
		t.Errorf("messages after bulk delete = %q", got)
	}
}

func TestInviteTargetUsers(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	ctx := t.Context()
	ch, err := c.CreateChannel(ctx, GuildID, discord.Payload{"name": "invites", "type": discord.ChannelTypeText})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := c.CreateInvite(ctx, ch.ID, discord.Payload{"target_user_ids": []string{"3", "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddInviteTargetUsers(ctx, inv.Code, []string{"2", "3"}); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveInviteTargetUsers(ctx, inv.Code, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetInviteTargetUsers(ctx, inv.Code)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"2", "3"}) {
		t.Errorf("target users = %v, want [2 3]", got)
	}
	if err := c.AddInviteTargetUsers(ctx, inv.Code, nil); apiErrorCode(err) != 50035 {
		t.Errorf("adding no users: %v, want code 50035", err)
	}
	if _, err := c.GetInviteTargetUsers(ctx, "missing"); !discord.IsNotFound(err) {
		t.Errorf("unknown invite: %v, want 404", err)
	}
	for _, body := range []discord.Payload{
		{"target_type": discord.InviteTargetStream},
		{"target_user_id": "1"},
		{"target_type": 3},
		{"role_ids": []string{GuildID}},
		{"target_user_ids": make([]string, 1001)},
	} {
		if _, err := c.CreateInvite(ctx, ch.ID, body); apiErrorCode(err) != 50035 {
			t.Errorf("create invite with %v: %v, want code 50035", body, err)
		}
	}
}
