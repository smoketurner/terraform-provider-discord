package discordtest

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

func TestReactions(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	ctx := t.Context()
	ch, err := c.CreateChannel(ctx, GuildID, discord.Payload{"name": "reactions", "type": discord.ChannelTypeText})
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.CreateMessage(ctx, ch.ID, discord.Payload{"content": "hi"})
	if err != nil {
		t.Fatal(err)
	}

	apiCode := func(err error) int {
		var apiErr *discord.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want an API error", err)
		}
		return apiErr.Code
	}
	if err := c.AddOwnReaction(ctx, ch.ID, "1", "👍"); !discord.IsNotFound(err) {
		t.Errorf("reacting to a missing message: err = %v, want 404", err)
	}
	for _, emoji := range []string{"thumbsup", "missing:123456", "a b"} {
		if code := apiCode(c.AddOwnReaction(ctx, ch.ID, m.ID, emoji)); code != 10014 {
			t.Errorf("AddOwnReaction(%q) code = %d, want 10014", emoji, code)
		}
	}

	// Reacting twice is a no-op, and a custom emoji is identified by
	// name:id.
	for range 2 {
		if err := c.AddOwnReaction(ctx, ch.ID, m.ID, "👍"); err != nil {
			t.Fatal(err)
		}
	}
	emoji, err := c.CreateEmoji(ctx, GuildID, discord.Payload{"name": "party", "image": "data:image/png;base64,AA=="})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddOwnReaction(ctx, ch.ID, m.ID, "party:"+emoji.ID); err != nil {
		t.Fatal(err)
	}
	if code := apiCode(c.AddOwnReaction(ctx, ch.ID, m.ID, "other:"+emoji.ID)); code != 10014 {
		t.Errorf("wrong custom emoji name: code = %d, want 10014", code)
	}
	if want := "PUT /channels/" + ch.ID + "/messages/" + m.ID + "/reactions/👍/@me"; !slices.Contains(s.Requests(), want) {
		t.Errorf("requests %v lack %q", s.Requests(), want)
	}

	for i := range discord.MaxReactionsPage {
		s.AddReaction(m.ID, "👍", strconv.Itoa(10000000000000000+i))
	}
	first, err := c.ListReactions(ctx, ch.ID, m.ID, "👍", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != discord.MaxReactionsPage || first[0].ID != "10000000000000000" {
		t.Fatalf("first page has %d users starting at %s", len(first), first[0].ID)
	}
	rest, err := c.ListReactions(ctx, ch.ID, m.ID, "👍", first[len(first)-1].ID)
	if err != nil {
		t.Fatal(err)
	}
	me, err := c.GetCurrentUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0].ID != me.ID || !rest[0].Bot {
		t.Errorf("second page = %+v, want only the bot %s", rest, me.ID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.URL+"/channels/"+ch.ID+"/messages/"+m.ID+"/reactions/%F0%9F%91%8D?limit=101", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bot "+Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("limit=101: status %d, want 400", res.StatusCode)
	}

	if err := c.DeleteOwnReaction(ctx, ch.ID, m.ID, "👍"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteOwnReaction(ctx, ch.ID, m.ID, "👍"); err != nil {
		t.Errorf("removing a missing reaction: %v", err)
	}
	if users, err := c.ListReactions(ctx, ch.ID, m.ID, "👍", first[len(first)-1].ID); err != nil || len(users) != 0 {
		t.Errorf("after removing: users = %+v, err = %v", users, err)
	}
	if err := c.DeleteMessage(ctx, ch.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListReactions(ctx, ch.ID, m.ID, "👍", ""); !discord.IsNotFound(err) {
		t.Errorf("deleted message: err = %v, want 404", err)
	}
}

func TestFollowChannel(t *testing.T) {
	s := NewServer()
	defer s.Close()
	c := discord.NewClient(s.URL, Token, "test")
	ctx := t.Context()
	news, err := c.CreateChannel(ctx, GuildID, discord.Payload{"name": "news", "type": discord.ChannelTypeAnnouncement})
	if err != nil {
		t.Fatal(err)
	}
	text, err := c.CreateChannel(ctx, GuildID, discord.Payload{"name": "text", "type": discord.ChannelTypeText})
	if err != nil {
		t.Fatal(err)
	}
	voice, err := c.CreateChannel(ctx, GuildID, discord.Payload{"name": "voice", "type": discord.ChannelTypeVoice})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ source, target string }{{text.ID, text.ID}, {news.ID, voice.ID}, {news.ID, "1"}} {
		if _, err := c.FollowChannel(ctx, tc.source, tc.target); err == nil {
			t.Errorf("FollowChannel(%s, %s) succeeded", tc.source, tc.target)
		}
	}
	f, err := c.FollowChannel(ctx, news.ID, text.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.ChannelID != news.ID {
		t.Errorf("followed channel = %s, want %s", f.ChannelID, news.ID)
	}
	w, err := c.GetFollowerWebhook(ctx, f.WebhookID)
	if err != nil {
		t.Fatal(err)
	}
	if w.Type != 2 || w.ChannelID != text.ID || w.GuildID != GuildID || w.SourceChannel == nil || w.SourceChannel.ID != news.ID {
		t.Errorf("webhook = %+v", w)
	}
	s.LoseFollowSource(f.WebhookID)
	if w, err = c.GetFollowerWebhook(ctx, f.WebhookID); err != nil || w.SourceChannel != nil {
		t.Errorf("after losing the source: webhook = %+v, err = %v", w, err)
	}
	if err := c.DeleteWebhook(ctx, f.WebhookID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetFollowerWebhook(ctx, f.WebhookID); !discord.IsNotFound(err) {
		t.Errorf("deleted webhook: err = %v, want 404", err)
	}
}
