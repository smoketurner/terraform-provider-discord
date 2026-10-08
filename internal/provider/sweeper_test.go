package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
	"github.com/smoketurner/terraform-provider-discord/internal/discord/discordtest"
)

// Live tests name every object they create with sweepPrefix, or with
// emojiSweepPrefix where Discord allows only letters, digits and underscores.
// The sweepers delete leftovers of failed or interrupted runs from the server
// in DISCORD_SERVER_ID, and never touch objects without the prefix. Run them
// with:
//
//	go test ./internal/provider -v -sweep=all -timeout 10m
const (
	sweepPrefix      = "tf-acc-"
	emojiSweepPrefix = "tf_acc_"
)

func TestMain(m *testing.M) {
	resource.TestMain(m)
}

// sweeper deletes the prefixed objects of one kind.
type sweeper struct {
	name string
	// dependencies are swept first.
	dependencies []string
	sweep        func(ctx context.Context, c *discord.Client, serverID string) error
}

// sweepers lists every kind of object live tests create. Threads, invites
// and webhooks are swept before channels, as some belong to channels without
// the prefix.
var sweepers = []sweeper{
	{name: "discord_thread", sweep: sweepThreads},
	{name: "discord_invite", sweep: sweepInvites},
	{name: "discord_webhook", sweep: sweepWebhooks},
	{name: "discord_channel", dependencies: []string{"discord_thread", "discord_invite", "discord_webhook"}, sweep: sweepChannels},
	{name: "discord_role", sweep: sweepRoles},
	{name: "discord_emoji", sweep: sweepEmojis},
	{name: "discord_sticker", sweep: sweepStickers},
	{name: "discord_soundboard_sound", sweep: sweepSounds},
	{name: "discord_scheduled_event", sweep: sweepScheduledEvents},
	{name: "discord_auto_moderation_rule", sweep: sweepAutoModerationRules},
	{name: "discord_server_template", sweep: sweepTemplates},
	{name: "discord_application_command", sweep: sweepApplicationCommands},
	{name: "discord_application_emoji", sweep: sweepApplicationEmojis},
}

func init() {
	for _, s := range sweepers {
		resource.AddTestSweepers(s.name, &resource.Sweeper{
			Name:         s.name,
			Dependencies: s.dependencies,
			F: func(string) error {
				token, serverID := os.Getenv("DISCORD_TOKEN"), os.Getenv("DISCORD_SERVER_ID")
				if token == "" || serverID == "" {
					return errors.New("sweeping needs DISCORD_TOKEN and DISCORD_SERVER_ID")
				}
				c := discord.NewClient(os.Getenv("DISCORD_BASE_URL"), token, "sweeper")
				return s.sweep(context.Background(), c, serverID)
			},
		})
	}
}

// sweepAll runs every sweeper in order.
func sweepAll(ctx context.Context, c *discord.Client, serverID string) error {
	var errs []error
	for _, s := range sweepers {
		errs = append(errs, s.sweep(ctx, c, serverID))
	}
	return errors.Join(errs...)
}

// sweepEach deletes the items whose name has the prefix, collecting errors
// other than objects already gone.
func sweepEach[T any](items []T, err error, prefix string, name func(T) string, del func(T) error) error {
	if err != nil {
		return err
	}
	var errs []error
	for _, item := range items {
		if !strings.HasPrefix(name(item), prefix) {
			continue
		}
		if err := del(item); err != nil && !discord.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("deleting %s: %w", name(item), err))
		}
	}
	return errors.Join(errs...)
}

func sweepThreads(ctx context.Context, c *discord.Client, serverID string) error {
	threadName := func(t discord.Thread) string { return t.Name }
	deleteThread := func(t discord.Thread) error { return c.DeleteChannel(ctx, t.ID) }
	active, err := c.ListActiveThreads(ctx, serverID)
	errs := []error{sweepEach(active, err, sweepPrefix, threadName, deleteThread)}
	channels, err := c.ListChannels(ctx, serverID)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, ch := range channels {
		switch ch.Type {
		case discord.ChannelTypeText:
			private, err := c.ListArchivedThreads(ctx, ch.ID, true)
			errs = append(errs, sweepEach(private, err, sweepPrefix, threadName, deleteThread))
			fallthrough
		case discord.ChannelTypeAnnouncement, discord.ChannelTypeForum, discord.ChannelTypeMedia:
			public, err := c.ListArchivedThreads(ctx, ch.ID, false)
			errs = append(errs, sweepEach(public, err, sweepPrefix, threadName, deleteThread))
		}
	}
	return errors.Join(errs...)
}

// sweepInvites deletes the invites to channels with the prefix.
func sweepInvites(ctx context.Context, c *discord.Client, serverID string) error {
	channels, err := c.ListChannels(ctx, serverID)
	if err != nil {
		return err
	}
	names := map[string]string{}
	for _, ch := range channels {
		names[ch.ID] = ch.Name
	}
	invites, err := c.ListGuildInvites(ctx, serverID)
	return sweepEach(invites, err, sweepPrefix, func(i discord.Invite) string {
		if i.Channel == nil {
			return ""
		}
		return names[i.Channel.ID]
	}, func(i discord.Invite) error { return c.DeleteInvite(ctx, i.Code) })
}

func sweepWebhooks(ctx context.Context, c *discord.Client, serverID string) error {
	webhooks, err := c.ListGuildWebhooks(ctx, serverID)
	return sweepEach(webhooks, err, sweepPrefix, func(w discord.Webhook) string {
		if w.Name == nil {
			return ""
		}
		return *w.Name
	}, func(w discord.Webhook) error { return c.DeleteWebhook(ctx, w.ID) })
}

// sweepChannels deletes categories last, so their channels are deleted
// while still listed under them.
func sweepChannels(ctx context.Context, c *discord.Client, serverID string) error {
	channels, err := c.ListChannels(ctx, serverID)
	if err != nil {
		return err
	}
	var others, categories []discord.Channel
	for _, ch := range channels {
		if ch.Type == discord.ChannelTypeCategory {
			categories = append(categories, ch)
		} else {
			others = append(others, ch)
		}
	}
	name := func(ch discord.Channel) string { return ch.Name }
	del := func(ch discord.Channel) error { return c.DeleteChannel(ctx, ch.ID) }
	return errors.Join(sweepEach(others, nil, sweepPrefix, name, del), sweepEach(categories, nil, sweepPrefix, name, del))
}

func sweepRoles(ctx context.Context, c *discord.Client, serverID string) error {
	roles, err := c.ListRoles(ctx, serverID)
	return sweepEach(roles, err, sweepPrefix, func(r discord.Role) string { return r.Name },
		func(r discord.Role) error { return c.DeleteRole(ctx, serverID, r.ID) })
}

func sweepEmojis(ctx context.Context, c *discord.Client, serverID string) error {
	emojis, err := c.ListEmojis(ctx, serverID)
	return sweepEach(emojis, err, emojiSweepPrefix, func(e discord.Emoji) string { return e.Name },
		func(e discord.Emoji) error { return c.DeleteEmoji(ctx, serverID, e.ID) })
}

func sweepStickers(ctx context.Context, c *discord.Client, serverID string) error {
	stickers, err := c.ListStickers(ctx, serverID)
	return sweepEach(stickers, err, sweepPrefix, func(s discord.Sticker) string { return s.Name },
		func(s discord.Sticker) error { return c.DeleteSticker(ctx, serverID, s.ID) })
}

func sweepSounds(ctx context.Context, c *discord.Client, serverID string) error {
	sounds, err := c.ListSoundboardSounds(ctx, serverID)
	return sweepEach(sounds, err, sweepPrefix, func(s discord.SoundboardSound) string { return s.Name },
		func(s discord.SoundboardSound) error { return c.DeleteSoundboardSound(ctx, serverID, s.SoundID) })
}

func sweepScheduledEvents(ctx context.Context, c *discord.Client, serverID string) error {
	events, err := c.ListScheduledEvents(ctx, serverID)
	return sweepEach(events, err, sweepPrefix, func(e discord.ScheduledEvent) string { return e.Name },
		func(e discord.ScheduledEvent) error { return c.DeleteScheduledEvent(ctx, serverID, e.ID) })
}

func sweepAutoModerationRules(ctx context.Context, c *discord.Client, serverID string) error {
	rules, err := c.ListAutoModerationRules(ctx, serverID)
	return sweepEach(rules, err, sweepPrefix, func(r discord.AutoModerationRule) string { return r.Name },
		func(r discord.AutoModerationRule) error { return c.DeleteAutoModerationRule(ctx, serverID, r.ID) })
}

func sweepTemplates(ctx context.Context, c *discord.Client, serverID string) error {
	templates, err := c.ListGuildTemplates(ctx, serverID)
	return sweepEach(templates, err, sweepPrefix, func(t discord.GuildTemplate) string { return t.Name },
		func(t discord.GuildTemplate) error { return c.DeleteGuildTemplate(ctx, serverID, t.Code) })
}

// sweepApplicationCommands deletes the bot's prefixed commands on the server
// and its prefixed global commands.
func sweepApplicationCommands(ctx context.Context, c *discord.Client, serverID string) error {
	app, err := c.GetCurrentApplication(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, guildID := range []string{serverID, ""} {
		commands, err := c.ListApplicationCommands(ctx, app.ID, guildID)
		errs = append(errs, sweepEach(commands, err, sweepPrefix, func(cmd discord.ApplicationCommand) string { return cmd.Name },
			func(cmd discord.ApplicationCommand) error {
				return c.DeleteApplicationCommand(ctx, app.ID, guildID, cmd.ID)
			}))
	}
	return errors.Join(errs...)
}

func sweepApplicationEmojis(ctx context.Context, c *discord.Client, _ string) error {
	app, err := c.GetCurrentApplication(ctx)
	if err != nil {
		return err
	}
	emojis, err := c.ListApplicationEmojis(ctx, app.ID)
	return sweepEach(emojis, err, emojiSweepPrefix, func(e discord.Emoji) string { return e.Name },
		func(e discord.Emoji) error { return c.DeleteApplicationEmoji(ctx, app.ID, e.ID) })
}

// TestSweep checks that the sweepers delete every prefixed object and leave
// the others.
func TestSweep(t *testing.T) {
	fake := discordtest.NewServer()
	defer fake.Close()
	c := discord.NewClient(fake.URL, discordtest.Token, "test")
	ctx := t.Context()
	must := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	app, err := c.GetCurrentApplication(ctx)
	must(app, err)
	server := discordtest.GuildID

	for _, name := range []string{"tf-acc-sweep", "keep"} {
		emojiName := strings.ReplaceAll(name, "-", "_")
		must(c.CreateRole(ctx, server, discord.Payload{"name": name}))
		must(c.CreateEmoji(ctx, server, discord.Payload{"name": emojiName, "image": onePixelPNG}))
		must(c.CreateApplicationEmoji(ctx, app.ID, discord.Payload{"name": emojiName, "image": onePixelPNG}))
		must(c.CreateSticker(ctx, server, &discord.Multipart{
			Payload: discord.Payload{"name": name, "tags": "wave"},
			Files:   []discord.File{{Field: "file", Name: "sticker.png", ContentType: "image/png", Data: []byte("\x89PNG\r\n\x1a\n")}},
		}))
		must(c.CreateSoundboardSound(ctx, server, discord.Payload{"name": name, "sound": soundMP3}))
		must(c.CreateScheduledEvent(ctx, server, discord.Payload{
			"name": name, "privacy_level": discord.PrivacyLevelGuildOnly, "entity_type": discord.ScheduledEventEntityExternal,
			"entity_metadata":      discord.Payload{"location": "Online"},
			"scheduled_start_time": eventTime(24 * time.Hour), "scheduled_end_time": eventTime(25 * time.Hour),
		}))
		must(c.CreateAutoModerationRule(ctx, server, discord.Payload{
			"name": name, "event_type": 1, "trigger_type": 1, "trigger_metadata": discord.Payload{"keyword_filter": []string{name}},
			"actions": []discord.Payload{{"type": 1}},
		}))
		for _, guild := range []string{server, ""} {
			must(c.CreateApplicationCommand(ctx, app.ID, guild, discord.Payload{"name": name, "type": 2}))
		}
		category, err := c.CreateChannel(ctx, server, discord.Payload{"name": name, "type": discord.ChannelTypeCategory})
		must(category, err)
		channel, err := c.CreateChannel(ctx, server, discord.Payload{"name": name, "type": discord.ChannelTypeText, "parent_id": category.ID})
		must(channel, err)
		must(c.CreateInvite(ctx, channel.ID, discord.Payload{}))
		must(c.CreateWebhook(ctx, channel.ID, discord.Payload{"name": name}))
		must(c.StartThread(ctx, channel.ID, discord.Payload{"name": name, "type": 11}))
		archived, err := c.StartThread(ctx, channel.ID, discord.Payload{"name": name + "-archived", "type": 12})
		must(archived, err)
		must(c.ModifyThread(ctx, archived.ID, discord.Payload{"archived": true}))
	}
	// A server has one template, so only a prefixed one is checked.
	must(c.CreateGuildTemplate(ctx, server, discord.Payload{"name": "tf-acc-sweep"}))

	if err := sweepAll(ctx, c, server); err != nil {
		t.Fatal(err)
	}

	var names []string
	add := func(prefix, name string) {
		if strings.HasPrefix(name, prefix) || strings.HasPrefix(name, "keep") {
			names = append(names, name)
		}
	}
	roles, err := c.ListRoles(ctx, server)
	must(roles, err)
	for _, r := range roles {
		add(sweepPrefix, r.Name)
	}
	emojis, err := c.ListEmojis(ctx, server)
	must(emojis, err)
	appEmojis, err := c.ListApplicationEmojis(ctx, app.ID)
	must(appEmojis, err)
	for _, e := range slices.Concat(emojis, appEmojis) {
		add(emojiSweepPrefix, e.Name)
	}
	stickers, err := c.ListStickers(ctx, server)
	must(stickers, err)
	for _, s := range stickers {
		add(sweepPrefix, s.Name)
	}
	sounds, err := c.ListSoundboardSounds(ctx, server)
	must(sounds, err)
	for _, s := range sounds {
		add(sweepPrefix, s.Name)
	}
	events, err := c.ListScheduledEvents(ctx, server)
	must(events, err)
	for _, e := range events {
		add(sweepPrefix, e.Name)
	}
	rules, err := c.ListAutoModerationRules(ctx, server)
	must(rules, err)
	for _, r := range rules {
		add(sweepPrefix, r.Name)
	}
	for _, guild := range []string{server, ""} {
		commands, err := c.ListApplicationCommands(ctx, app.ID, guild)
		must(commands, err)
		for _, cmd := range commands {
			add(sweepPrefix, cmd.Name)
		}
	}
	channels, err := c.ListChannels(ctx, server)
	must(channels, err)
	var keptChannel string
	for _, ch := range channels {
		add(sweepPrefix, ch.Name)
		if ch.Type == discord.ChannelTypeText && ch.Name == "keep" {
			keptChannel = ch.ID
		}
	}
	webhooks, err := c.ListGuildWebhooks(ctx, server)
	must(webhooks, err)
	for _, w := range webhooks {
		add(sweepPrefix, *w.Name)
	}
	invites, err := c.ListGuildInvites(ctx, server)
	must(invites, err)
	if len(invites) != 1 || invites[0].Channel == nil || invites[0].Channel.ID != keptChannel {
		t.Errorf("invites = %+v, want only the invite to the kept channel %s", invites, keptChannel)
	}
	active, err := c.ListActiveThreads(ctx, server)
	must(active, err)
	archivedThreads, err := c.ListArchivedThreads(ctx, keptChannel, true)
	must(archivedThreads, err)
	for _, th := range slices.Concat(active, archivedThreads) {
		add(sweepPrefix, th.Name)
	}
	templates, err := c.ListGuildTemplates(ctx, server)
	must(templates, err)
	for _, tmpl := range templates {
		add(sweepPrefix, tmpl.Name)
	}

	slices.Sort(names)
	// Kept: a role, two emojis, a sticker, a sound, an event, a rule, two
	// commands, a category, a channel, a webhook and two threads.
	want := slices.Concat(slices.Repeat([]string{"keep"}, 13), []string{"keep-archived"})
	if !slices.Equal(names, want) {
		t.Errorf("objects after sweeping = %v, want %v", names, want)
	}
}
