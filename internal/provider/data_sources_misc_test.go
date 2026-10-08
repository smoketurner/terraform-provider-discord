package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
	"github.com/smoketurner/terraform-provider-discord/internal/discord/discordtest"
)

// countRequests returns a check that methodPath was requested want times
// since the count was last taken.
func (e *testEnv) countRequests(methodPath string, want int) resource.TestCheckFunc {
	seen := 0
	return func(*terraform.State) error {
		n := 0
		for _, r := range e.fake.Requests() {
			if r == methodPath {
				n++
			}
		}
		got := n - seen
		seen = n
		if got < want {
			return fmt.Errorf("%s requested %d times, want at least %d", methodPath, got, want)
		}
		return nil
	}
}

func TestAccVoiceRegionsDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	config := env.config(`
data "discord_voice_regions" "all" {}
data "discord_voice_regions" "server" {
  server_id = local.server_id
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_voice_regions.all", "regions.#", "3"),
					resource.TestCheckResourceAttr("data.discord_voice_regions.all", "regions.0.id", "us-east"),
					resource.TestCheckResourceAttr("data.discord_voice_regions.all", "regions.0.optimal", "true"),
					resource.TestCheckResourceAttr("data.discord_voice_regions.all", "regions.2.deprecated", "true"),
					resource.TestCheckResourceAttr("data.discord_voice_regions.server", "regions.#", "3"),
				),
			},
			{
				// Servers with VIP regions list them in addition.
				PreConfig: func() { env.fake.SetGuildFeatures("VIP_REGIONS") },
				Config:    config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_voice_regions.all", "regions.#", "3"),
					resource.TestCheckResourceAttr("data.discord_voice_regions.server", "regions.#", "4"),
					resource.TestCheckResourceAttr("data.discord_voice_regions.server", "regions.3.id", discordtest.VIPVoiceRegion.ID),
					resource.TestCheckResourceAttr("data.discord_voice_regions.server", "regions.3.custom", "true"),
				),
			},
			{
				Config:      env.config(`data "discord_voice_regions" "bad" { server_id = "us-east" }`),
				ExpectError: regexp.MustCompile(`must be a Discord snowflake ID`),
			},
			{
				Config:      env.config(`data "discord_voice_regions" "missing" { server_id = "999999999999999999" }`),
				ExpectError: regexp.MustCompile(`Unknown\s+Guild`),
			},
		},
	})
}

func TestAccServerPreviewDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
data "discord_server_preview" "test" {
  id = local.server_id
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_server_preview.test", "name", "Test Server"),
					resource.TestCheckResourceAttr("data.discord_server_preview.test", "features.#", "2"),
					resource.TestCheckTypeSetElemAttr("data.discord_server_preview.test", "features.*", "COMMUNITY"),
					resource.TestCheckResourceAttr("data.discord_server_preview.test", "approximate_member_count", "1"),
					resource.TestCheckResourceAttr("data.discord_server_preview.test", "emojis.#", "0"),
					resource.TestCheckResourceAttr("data.discord_server_preview.test", "stickers.#", "0"),
					resource.TestCheckNoResourceAttr("data.discord_server_preview.test", "description"),
				),
			},
			{
				// Members joining outside Terraform show in the next read.
				PreConfig: func() { env.fake.AddMember(env.serverID, "newcomer") },
				Config: env.config(`
data "discord_server_preview" "test" {
  id = local.server_id
}`),
				Check: resource.TestCheckResourceAttr("data.discord_server_preview.test", "approximate_member_count", "2"),
			},
			{
				Config:      env.config(`data "discord_server_preview" "test" { id = "999999999999999999" }`),
				ExpectError: regexp.MustCompile(`(?s)must\s+be\s+discoverable.*Unknown\s+Guild`),
			},
		},
	})
}

func TestAccServerVanityURLDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	config := env.config(`
data "discord_server_vanity_url" "test" {
  server_id = local.server_id
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`Missing\s+Permissions`),
			},
			{
				PreConfig: func() { env.fake.SetGuildFeatures("VANITY_URL") },
				Config:    config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("data.discord_server_vanity_url.test", "code"),
					resource.TestCheckNoResourceAttr("data.discord_server_vanity_url.test", "url"),
					resource.TestCheckResourceAttr("data.discord_server_vanity_url.test", "uses", "0"),
				),
			},
			{
				PreConfig: func() { env.fake.SetVanityURL(env.serverID, "tf-acc", 12) },
				Config:    config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_server_vanity_url.test", "code", "tf-acc"),
					resource.TestCheckResourceAttr("data.discord_server_vanity_url.test", "url", "https://discord.gg/tf-acc"),
					resource.TestCheckResourceAttr("data.discord_server_vanity_url.test", "uses", "12"),
				),
			},
		},
	})
}

func TestAccServerWidgetDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	channels := `
resource "discord_text_channel" "invite" {
  server_id = local.server_id
  name      = "tf-acc-widget-invite"
}
resource "discord_voice_channel" "lobby" {
  server_id = local.server_id
  name      = "tf-acc-widget-lobby"
}
`
	read := `
data "discord_server_widget" "test" {
  server_id  = local.server_id
  depends_on = [discord_server_widget.test]
}`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(channels + `
resource "discord_server_widget" "test" {
  server_id = local.server_id
  enabled   = false
}` + read),
				ExpectError: regexp.MustCompile(`(?s)widget\s+must\s+be\s+enabled.*Widget\s+Disabled`),
			},
			{
				Config: env.config(channels + `
resource "discord_server_widget" "test" {
  server_id  = local.server_id
  enabled    = true
  channel_id = discord_text_channel.invite.id
}` + read),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_server_widget.test", "name", "Test Server"),
					resource.TestCheckResourceAttrSet("data.discord_server_widget.test", "instant_invite"),
					resource.TestCheckResourceAttr("data.discord_server_widget.test", "channels.#", "1"),
					resource.TestCheckResourceAttrPair("data.discord_server_widget.test", "channels.0.id", "discord_voice_channel.lobby", "id"),
					resource.TestCheckResourceAttr("data.discord_server_widget.test", "channels.0.name", "tf-acc-widget-lobby"),
					resource.TestCheckResourceAttr("data.discord_server_widget.test", "presence_count", "1"),
					resource.TestCheckResourceAttr("data.discord_server_widget.test", "members.0.status", "online"),
				),
			},
			{
				Config: env.config(channels + `
resource "discord_server_widget" "test" {
  server_id = local.server_id
  enabled   = true
}` + read),
				Check: resource.TestCheckNoResourceAttr("data.discord_server_widget.test", "instant_invite"),
			},
		},
	})
}

func TestAccInviteDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	channelID := env.seedChannel("tf-acc-invite-lookup", discord.ChannelTypeText, 0, "")
	inv, err := env.client.CreateInvite(context.Background(), channelID, discord.Payload{"max_age": 0})
	if err != nil {
		t.Fatal(err)
	}
	config := env.config(`
data "discord_invite" "test" {
  code = "` + inv.Code + `"
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
data "discord_invite" "test" {
  code               = "abc"
  scheduled_event_id = "event"
}`),
				ExpectError: regexp.MustCompile(`must be a Discord snowflake ID`),
			},
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_invite.test", "type", "guild"),
					resource.TestCheckResourceAttr("data.discord_invite.test", "server_id", env.serverID),
					resource.TestCheckResourceAttr("data.discord_invite.test", "server_name", "Test Server"),
					resource.TestCheckResourceAttr("data.discord_invite.test", "channel_id", channelID),
					resource.TestCheckResourceAttr("data.discord_invite.test", "channel_name", "tf-acc-invite-lookup"),
					resource.TestCheckResourceAttrSet("data.discord_invite.test", "inviter_id"),
					resource.TestCheckResourceAttr("data.discord_invite.test", "approximate_member_count", "1"),
					resource.TestCheckResourceAttr("data.discord_invite.test", "approximate_presence_count", "1"),
					resource.TestCheckNoResourceAttr("data.discord_invite.test", "expires_at"),
					resource.TestCheckNoResourceAttr("data.discord_invite.test", "target_type"),
					resource.TestCheckNoResourceAttr("data.discord_invite.test", "target_user_id"),
				),
			},
			{
				// An invite revoked outside Terraform no longer resolves.
				PreConfig:   func() { env.fake.DeleteInvite(inv.Code) },
				Config:      config,
				ExpectError: regexp.MustCompile(`Unknown\s+Invite`),
			},
		},
	})
}

func TestAccMessageDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	ctx := context.Background()
	channelID := env.seedChannel("tf-acc-message-lookup", discord.ChannelTypeText, 0, "")
	msg, err := env.client.CreateMessage(ctx, channelID, discord.Payload{"content": "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.client.PinMessage(ctx, channelID, msg.ID); err != nil {
		t.Fatal(err)
	}
	config := env.config(`
data "discord_message" "test" {
  channel_id = "` + channelID + `"
  id         = "` + msg.ID + `"
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
data "discord_message" "test" {
  channel_id = "` + channelID + `"
}`),
				ExpectError: regexp.MustCompile(`The argument "id" is required`),
			},
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_message.test", "content", "Hello"),
					resource.TestCheckResourceAttr("data.discord_message.test", "pinned", "true"),
					resource.TestCheckResourceAttr("data.discord_message.test", "type", "0"),
					resource.TestCheckResourceAttr("data.discord_message.test", "flags", "0"),
					resource.TestCheckResourceAttr("data.discord_message.test", "author_id", msg.Author.ID),
					resource.TestCheckResourceAttr("data.discord_message.test", "author_username", msg.Author.Username),
					resource.TestCheckResourceAttr("data.discord_message.test", "timestamp", msg.Timestamp),
					resource.TestCheckNoResourceAttr("data.discord_message.test", "edited_timestamp"),
					resource.TestCheckNoResourceAttr("data.discord_message.test", "webhook_id"),
				),
			},
			{
				// The data source reads changes made outside Terraform.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					if _, err := c.EditMessage(ctx, channelID, msg.ID, discord.Payload{"content": "Edited"}); err != nil {
						return err
					}
					return c.UnpinMessage(ctx, channelID, msg.ID)
				}),
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_message.test", "content", "Edited"),
					resource.TestCheckResourceAttr("data.discord_message.test", "pinned", "false"),
					resource.TestCheckResourceAttrSet("data.discord_message.test", "edited_timestamp"),
				),
			},
			{
				PreConfig:   func() { env.fake.DeleteMessage(msg.ID) },
				Config:      config,
				ExpectError: regexp.MustCompile(`Unknown\s+Message`),
			},
		},
	})
}

func TestAccPinnedMessagesDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	channelID := env.seedChannel("tf-acc-pins", discord.ChannelTypeText, 0, "")
	ctx := context.Background()
	var ids []string
	for i := range 55 {
		m, err := env.client.CreateMessage(ctx, channelID, discord.Payload{"content": "pin " + strconv.Itoa(i)})
		if err != nil {
			t.Fatal(err)
		}
		if err := env.client.PinMessage(ctx, channelID, m.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if _, err := env.client.CreateMessage(ctx, channelID, discord.Payload{"content": "not pinned"}); err != nil {
		t.Fatal(err)
	}
	config := env.config(`
data "discord_pinned_messages" "test" {
  channel_id = "` + channelID + `"
}`)
	pins := "GET /channels/" + channelID + "/messages/pins"
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				// 55 pins take two pages of 50.
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_pinned_messages.test", "messages.#", "55"),
					resource.TestCheckResourceAttr("data.discord_pinned_messages.test", "messages.0.id", ids[54]),
					resource.TestCheckResourceAttr("data.discord_pinned_messages.test", "messages.0.content", "pin 54"),
					resource.TestCheckResourceAttr("data.discord_pinned_messages.test", "messages.0.pinned", "true"),
					resource.TestCheckResourceAttrSet("data.discord_pinned_messages.test", "messages.0.pinned_at"),
					resource.TestCheckResourceAttr("data.discord_pinned_messages.test", "messages.54.id", ids[0]),
					resource.TestCheckResourceAttr("data.discord_pinned_messages.test", "messages.50.id", ids[4]),
					env.countRequests(pins, 2),
				),
			},
			{
				// Unpinning outside Terraform removes the message.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.UnpinMessage(ctx, channelID, ids[54])
				}),
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_pinned_messages.test", "messages.#", "54"),
					resource.TestCheckResourceAttr("data.discord_pinned_messages.test", "messages.0.id", ids[53]),
				),
			},
			{
				Config:      env.config(`data "discord_pinned_messages" "test" { channel_id = "999999999999999999" }`),
				ExpectError: regexp.MustCompile(`Unknown\s+Channel`),
			},
		},
	})
}

func TestAccPinnedMessagesDataSourceEmpty(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-no-pins"
}
data "discord_pinned_messages" "test" {
  channel_id = discord_text_channel.test.id
}`),
			Check: resource.TestCheckResourceAttr("data.discord_pinned_messages.test", "messages.#", "0"),
		}},
	})
}

func TestAccRoleMemberCountsDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireUser()
	resources := `
resource "discord_role" "member" {
  server_id = local.server_id
  name      = "tf-acc-counted"
}
resource "discord_role" "empty" {
  server_id = local.server_id
  name      = "tf-acc-uncounted"
}
resource "discord_member_role" "test" {
  server_id = local.server_id
  user_id   = local.user_id
  role_id   = discord_role.member.id
}
data "discord_role_member_counts" "test" {
  server_id  = local.server_id
  depends_on = [discord_member_role.test, discord_role.empty]
}
`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(resources),
			Check: resource.ComposeAggregateTestCheckFunc(
				func(s *terraform.State) error {
					ds := s.RootModule().Resources["data.discord_role_member_counts.test"].Primary.Attributes
					member := s.RootModule().Resources["discord_role.member"].Primary.ID
					empty := s.RootModule().Resources["discord_role.empty"].Primary.ID
					if got := ds["counts."+member]; got != "1" {
						return fmt.Errorf("count of %s = %q, want 1", member, got)
					}
					if got := ds["counts."+empty]; got != "0" {
						return fmt.Errorf("count of %s = %q, want 0", empty, got)
					}
					if _, ok := ds["counts."+env.serverID]; ok {
						return errors.New("counts include @everyone")
					}
					return nil
				},
			),
		}},
	})
}

func TestAccStickerDataSources(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	pack := discordtest.StickerPacks[0]
	sticker := pack.Stickers[0]
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
data "discord_sticker_packs" "all" {}
data "discord_sticker_pack" "test" {
  id = "` + pack.ID + `"
}
data "discord_sticker" "test" {
  id = "` + sticker.ID + `"
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_sticker_packs.all", "packs.#", strconv.Itoa(len(discordtest.StickerPacks))),
					resource.TestCheckResourceAttr("data.discord_sticker_packs.all", "packs.0.name", pack.Name),
					resource.TestCheckResourceAttr("data.discord_sticker_packs.all", "packs.0.stickers.#", "2"),
					resource.TestCheckResourceAttr("data.discord_sticker_packs.all", "packs.1.stickers.#", "0"),
					resource.TestCheckNoResourceAttr("data.discord_sticker_packs.all", "packs.1.cover_sticker_id"),
					resource.TestCheckResourceAttr("data.discord_sticker_pack.test", "name", pack.Name),
					resource.TestCheckResourceAttr("data.discord_sticker_pack.test", "sku_id", pack.SKUID),
					resource.TestCheckResourceAttr("data.discord_sticker_pack.test", "cover_sticker_id", *pack.CoverStickerID),
					resource.TestCheckResourceAttr("data.discord_sticker_pack.test", "banner_asset_id", *pack.BannerAssetID),
					resource.TestCheckResourceAttr("data.discord_sticker_pack.test", "stickers.1.format_type", "png"),
					resource.TestCheckNoResourceAttr("data.discord_sticker_pack.test", "stickers.1.description"),
					resource.TestCheckResourceAttr("data.discord_sticker.test", "name", sticker.Name),
					resource.TestCheckResourceAttr("data.discord_sticker.test", "description", *sticker.Description),
					resource.TestCheckResourceAttr("data.discord_sticker.test", "type", "standard"),
					resource.TestCheckResourceAttr("data.discord_sticker.test", "format_type", "lottie"),
					resource.TestCheckResourceAttr("data.discord_sticker.test", "pack_id", pack.ID),
					resource.TestCheckResourceAttr("data.discord_sticker.test", "sort_value", "1"),
					resource.TestCheckNoResourceAttr("data.discord_sticker.test", "server_id"),
					resource.TestCheckNoResourceAttr("data.discord_sticker.test", "available"),
				),
			},
			{
				Config:      env.config(`data "discord_sticker_pack" "test" { id = "999999999999999999" }`),
				ExpectError: regexp.MustCompile(`Unknown\s+Sticker\s+Pack`),
			},
			{
				Config:      env.config(`data "discord_sticker" "test" { id = "999999999999999999" }`),
				ExpectError: regexp.MustCompile(`Unknown\s+Sticker`),
			},
		},
	})
}

func TestAccDefaultSoundboardSoundsDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: `data "discord_default_soundboard_sounds" "test" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.discord_default_soundboard_sounds.test", "sounds.#", "2"),
				resource.TestCheckResourceAttr("data.discord_default_soundboard_sounds.test", "sounds.0.name", "quack"),
				resource.TestCheckResourceAttr("data.discord_default_soundboard_sounds.test", "sounds.0.volume", "1"),
				resource.TestCheckResourceAttr("data.discord_default_soundboard_sounds.test", "sounds.1.volume", "0.5"),
				resource.TestCheckResourceAttr("data.discord_default_soundboard_sounds.test", "sounds.1.emoji_name", "🔊"),
				resource.TestCheckNoResourceAttr("data.discord_default_soundboard_sounds.test", "sounds.1.emoji_id"),
			),
		}},
	})
}

func TestAccAuditLogDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	alice, bob := "100000000000000010", "100000000000000011"
	var ids []string
	for i := range 120 {
		user, action := alice, int64(22)
		if i%2 == 1 {
			user, action = bob, 25
		}
		e := discord.AuditLogEntry{UserID: &user, TargetID: new("100000000000000099"), ActionType: action}
		if i == 119 {
			e.Reason = new("Spam")
			e.Options = map[string]json.RawMessage{"channel_id": json.RawMessage(`"100000000000000050"`), "count": json.RawMessage(`3`)}
			e.Changes = []discord.AuditLogChange{
				{Key: "name", OldValue: json.RawMessage(`"old"`), NewValue: json.RawMessage(`"new"`)},
				{Key: "$add", NewValue: json.RawMessage(`[{"id": "1", "name": "role"}]`)},
				{Key: "topic", OldValue: json.RawMessage(`"gone"`)},
			}
		}
		ids = append(ids, env.fake.AddAuditLogEntry(env.serverID, e))
	}
	auditLog := "GET /guilds/" + env.serverID + "/audit-logs"
	lookup := func(name, attrs string) string {
		return `
data "discord_audit_log" "` + name + `" {
  server_id = local.server_id
` + attrs + `
}`
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(lookup("recent", "") + lookup("many", "limit = 110")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.#", "50"),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.0.id", ids[119]),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.0.action_type", "25"),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.0.user_id", bob),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.0.target_id", "100000000000000099"),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.0.reason", "Spam"),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.0.options.channel_id", "100000000000000050"),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.0.options.count", "3"),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.0.changes.#", "3"),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.0.changes.0.key", "name"),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.0.changes.0.old_value", `"old"`),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.0.changes.0.new_value", `"new"`),
					resource.TestCheckNoResourceAttr("data.discord_audit_log.recent", "entries.0.changes.1.old_value"),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.0.changes.1.new_value", `[{"id":"1","name":"role"}]`),
					resource.TestCheckNoResourceAttr("data.discord_audit_log.recent", "entries.0.changes.2.new_value"),
					resource.TestCheckNoResourceAttr("data.discord_audit_log.recent", "entries.1.reason"),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.1.options.%", "0"),
					resource.TestCheckResourceAttr("data.discord_audit_log.recent", "entries.1.changes.#", "0"),
					resource.TestCheckResourceAttr("data.discord_audit_log.many", "entries.#", "110"),
					resource.TestCheckResourceAttr("data.discord_audit_log.many", "entries.100.id", ids[19]),
					resource.TestCheckResourceAttr("data.discord_audit_log.many", "entries.109.id", ids[10]),
					// One request for the default 50, two for 110.
					env.countRequests(auditLog, 3),
				),
			},
			{
				Config: env.config(lookup("alice", "user_id = \""+alice+"\"\n  limit = 200") +
					lookup("roles", "action_type = 25\n  before = \""+ids[100]+"\"\n  limit = 5") +
					lookup("oldest", "after = \"0\"\n  limit = 101") +
					lookup("between", "after = \""+ids[10]+"\"\n  before = \""+ids[14]+"\"")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_audit_log.alice", "entries.#", "60"),
					resource.TestCheckResourceAttr("data.discord_audit_log.alice", "entries.0.id", ids[118]),
					resource.TestCheckResourceAttr("data.discord_audit_log.alice", "entries.0.action_type", "22"),
					resource.TestCheckResourceAttr("data.discord_audit_log.roles", "entries.#", "5"),
					resource.TestCheckResourceAttr("data.discord_audit_log.roles", "entries.0.id", ids[99]),
					resource.TestCheckResourceAttr("data.discord_audit_log.roles", "entries.4.id", ids[91]),
					resource.TestCheckResourceAttr("data.discord_audit_log.oldest", "entries.#", "101"),
					resource.TestCheckResourceAttr("data.discord_audit_log.oldest", "entries.0.id", ids[0]),
					resource.TestCheckResourceAttr("data.discord_audit_log.oldest", "entries.100.id", ids[100]),
					resource.TestCheckResourceAttr("data.discord_audit_log.between", "entries.#", "3"),
					resource.TestCheckResourceAttr("data.discord_audit_log.between", "entries.0.id", ids[11]),
				),
			},
			{
				// Entries added outside Terraform appear on the next read.
				PreConfig: func() {
					ids = append(ids, env.fake.AddAuditLogEntry(env.serverID, discord.AuditLogEntry{ActionType: 1}))
				},
				Config: env.config(lookup("latest", "limit = 1")),
				Check: resource.ComposeAggregateTestCheckFunc(
					func(s *terraform.State) error {
						got := s.RootModule().Resources["data.discord_audit_log.latest"].Primary.Attributes["entries.0.id"]
						if got != ids[len(ids)-1] {
							return fmt.Errorf("latest entry = %s, want %s", got, ids[len(ids)-1])
						}
						return nil
					},
					resource.TestCheckNoResourceAttr("data.discord_audit_log.latest", "entries.0.user_id"),
				),
			},
			{
				Config:      env.config(lookup("bad", "limit = 0")),
				ExpectError: regexp.MustCompile(`must be at least 1`),
			},
			{
				Config:      env.config(lookup("bad", "action_type = 0")),
				ExpectError: regexp.MustCompile(`must be at least 1`),
			},
			{
				Config:      env.config(lookup("bad", `before = "yesterday"`)),
				ExpectError: regexp.MustCompile(`must be a Discord snowflake ID`),
			},
			{
				Config:      env.config(`data "discord_audit_log" "bad" { server_id = "999999999999999999" }`),
				ExpectError: regexp.MustCompile(`Unknown\s+Guild`),
			},
		},
	})
}

// The fake is used to page through more messages than live tests should
// post.
func TestAccMessagesDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	ctx := context.Background()
	ch, err := env.client.CreateChannel(ctx, env.serverID, discord.Payload{"name": "tf-acc-messages", "type": discord.ChannelTypeText})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range 130 {
		msg, err := env.client.CreateMessage(ctx, ch.ID, discord.Payload{"content": fmt.Sprintf("message %d", i)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, msg.ID)
	}
	lookup := func(name, attrs string) string {
		return `
data "discord_messages" "` + name + `" {
  channel_id = "` + ch.ID + `"
` + attrs + `
}`
	}
	list := "GET /channels/" + ch.ID + "/messages"
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      env.config(lookup("conflict", `around = "`+ids[5]+`"`+"\n  before = \""+ids[9]+`"`)),
				ExpectError: regexp.MustCompile(`Invalid Attribute Combination`),
			},
			{
				Config:      env.config(lookup("around", `around = "`+ids[5]+`"`+"\n  limit = 101")),
				ExpectError: regexp.MustCompile(`With around, limit must be at most 100`),
			},
			{
				Config:      env.config(lookup("zero", "limit = 0")),
				ExpectError: regexp.MustCompile(`limit value must be at least 1`),
			},
			{
				Config:      env.config(`data "discord_messages" "missing" { channel_id = "100000000000000099" }`),
				ExpectError: regexp.MustCompile(`Unknown\s+Channel`),
			},
			{
				// The default limit of 50 returns the newest 50, newest first.
				Config: env.config(lookup("recent", "") + lookup("all", "limit = 1000")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_messages.recent", "messages.#", "50"),
					resource.TestCheckResourceAttr("data.discord_messages.recent", "messages.0.id", ids[129]),
					resource.TestCheckResourceAttr("data.discord_messages.recent", "messages.0.content", "message 129"),
					resource.TestCheckResourceAttr("data.discord_messages.recent", "messages.0.type", "0"),
					resource.TestCheckResourceAttrSet("data.discord_messages.recent", "messages.0.author_id"),
					resource.TestCheckResourceAttrSet("data.discord_messages.recent", "messages.0.timestamp"),
					resource.TestCheckResourceAttr("data.discord_messages.recent", "messages.49.id", ids[80]),
					resource.TestCheckResourceAttr("data.discord_messages.all", "messages.#", "130"),
					resource.TestCheckResourceAttr("data.discord_messages.all", "messages.0.id", ids[129]),
					resource.TestCheckResourceAttr("data.discord_messages.all", "messages.129.id", ids[0]),
					// One request for the newest 50, two for all 130.
					env.countRequests(list, 3),
				),
			},
			{
				Config: env.config(lookup("before", `before = "`+ids[10]+`"`) +
					lookup("after", `after = "`+ids[119]+`"`+"\n  limit = 5") +
					lookup("oldest", `after = "0"`+"\n  limit = 120") +
					lookup("around", `around = "`+ids[50]+`"`+"\n  limit = 5") +
					lookup("empty", `after = "`+ids[129]+`"`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_messages.before", "messages.#", "10"),
					resource.TestCheckResourceAttr("data.discord_messages.before", "messages.0.id", ids[9]),
					resource.TestCheckResourceAttr("data.discord_messages.after", "messages.#", "5"),
					resource.TestCheckResourceAttr("data.discord_messages.after", "messages.0.id", ids[124]),
					resource.TestCheckResourceAttr("data.discord_messages.after", "messages.4.id", ids[120]),
					resource.TestCheckResourceAttr("data.discord_messages.oldest", "messages.#", "120"),
					resource.TestCheckResourceAttr("data.discord_messages.oldest", "messages.0.id", ids[119]),
					resource.TestCheckResourceAttr("data.discord_messages.oldest", "messages.119.id", ids[0]),
					resource.TestCheckResourceAttr("data.discord_messages.around", "messages.#", "5"),
					resource.TestCheckResourceAttr("data.discord_messages.around", "messages.0.id", ids[52]),
					resource.TestCheckResourceAttr("data.discord_messages.around", "messages.2.id", ids[50]),
					resource.TestCheckResourceAttr("data.discord_messages.around", "messages.4.id", ids[48]),
					resource.TestCheckResourceAttr("data.discord_messages.empty", "messages.#", "0"),
				),
			},
		},
	})
}
