package provider

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

func TestAccChannelsDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	category := env.seedChannel("tf-acc-category", discord.ChannelTypeCategory, 0, "")
	inCategory := env.seedChannel("tf-acc-in", discord.ChannelTypeText, 2, category)
	voice := env.seedChannel("tf-acc-voice", discord.ChannelTypeVoice, 1, category)
	outside := env.seedChannel("tf-acc-out", discord.ChannelTypeText, 1, "")
	hidden := env.seedChannel("tf-acc-hidden", discord.ChannelTypeText, 3, category)
	env.fake.HideChannel(hidden)
	other := env.seedChannel("tf-acc-other", discord.ChannelTypeCategory, 5, "")
	env.seedChannel("tf-acc-in-other", discord.ChannelTypeText, 6, other)
	cfg := env.config(`
data "discord_channels" "all" {
  server_id = local.server_id
}
data "discord_channels" "text" {
  server_id = local.server_id
  type      = "text"
}
data "discord_channels" "category" {
  server_id   = local.server_id
  category_id = "` + category + `"
}
data "discord_channels" "text_in_category" {
  server_id   = local.server_id
  type        = "text"
  category_id = "` + category + `"
}
data "discord_channels" "none" {
  server_id = local.server_id
  type      = "forum"
}`)
	var added string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
data "discord_channels" "test" {
  server_id = local.server_id
  type      = "thread"
}`),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
			{
				Config: env.config(`
data "discord_channels" "test" {
  server_id   = local.server_id
  category_id = "general"
}`),
				ExpectError: regexp.MustCompile(`must be a Discord snowflake ID`),
			},
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_channels.all", "channels.#", "6"),
					resource.TestCheckResourceAttr("data.discord_channels.all", "channels.0.id", category),
					resource.TestCheckResourceAttr("data.discord_channels.all", "channels.0.type", "category"),
					resource.TestCheckResourceAttr("data.discord_channels.all", "channels.1.id", voice),
					resource.TestCheckResourceAttr("data.discord_channels.all", "channels.2.id", outside),
					resource.TestCheckResourceAttr("data.discord_channels.all", "channels.3.id", inCategory),
					resource.TestCheckResourceAttr("data.discord_channels.all", "channels.3.category_id", category),
					resource.TestCheckResourceAttr("data.discord_channels.all", "channels.3.name", "tf-acc-in"),
					resource.TestCheckResourceAttr("data.discord_channels.all", "channels.3.position", "2"),
					resource.TestCheckResourceAttr("data.discord_channels.all", "channels.3.nsfw", "false"),
					resource.TestCheckNoResourceAttr("data.discord_channels.all", "channels.2.category_id"),
					resource.TestCheckResourceAttr("data.discord_channels.text", "channels.#", "3"),
					resource.TestCheckResourceAttr("data.discord_channels.category", "channels.#", "2"),
					resource.TestCheckResourceAttr("data.discord_channels.category", "channels.0.id", voice),
					resource.TestCheckResourceAttr("data.discord_channels.text_in_category", "channels.#", "1"),
					resource.TestCheckResourceAttr("data.discord_channels.text_in_category", "channels.0.id", inCategory),
					resource.TestCheckResourceAttr("data.discord_channels.none", "channels.#", "0"),
				),
			},
			{
				// A channel created outside Terraform shows up on the next read.
				PreConfig: func() {
					added = env.seedChannel("tf-acc-added", discord.ChannelTypeText, 5, category)
				},
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_channels.all", "channels.#", "7"),
					resource.TestCheckResourceAttrPtr("data.discord_channels.text_in_category", "channels.1.id", &added),
				),
			},
		},
	})
}

func TestAccRolesDataSource(t *testing.T) {
	env := newTestEnv(t)
	cfg := env.config(`
resource "discord_role" "test" {
  server_id   = local.server_id
  name        = "tf-acc-roles"
  permissions = "8"
  color       = 255
}
data "discord_roles" "test" {
  server_id  = local.server_id
  depends_on = [discord_role.test]
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.discord_roles.test", "roles.0.name", "@everyone"),
				resource.TestCheckResourceAttr("data.discord_roles.test", "roles.0.id", env.serverID),
				resource.TestCheckTypeSetElemNestedAttrs("data.discord_roles.test", "roles.*", map[string]string{
					"name":        "tf-acc-roles",
					"permissions": "8",
					"color":       "255",
					"managed":     "false",
				}),
			),
		}},
	})
}

func TestAccMembersDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	ids := []string{env.userID}
	for i := range discord.MaxPageSize {
		ids = append(ids, env.fake.AddMember(env.serverID, "member"+strconv.Itoa(i)))
	}
	members := func(attrs string) string {
		return env.config(`
data "discord_members" "test" {
  server_id = local.server_id
  ` + attrs + `
}`)
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      members("limit = 0"),
				ExpectError: regexp.MustCompile(`must be at least 1`),
			},
			{
				// 1001 members take two pages.
				Config: members(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_members.test", "members.#", "1001"),
					resource.TestCheckResourceAttr("data.discord_members.test", "members.0.user_id", env.userID),
					resource.TestCheckResourceAttr("data.discord_members.test", "members.0.username", "tester"),
					resource.TestCheckResourceAttr("data.discord_members.test", "members.0.bot", "false"),
					resource.TestCheckResourceAttr("data.discord_members.test", "members.0.roles.#", "0"),
					resource.TestCheckResourceAttr("data.discord_members.test", "members.1000.user_id", ids[1000]),
					resource.TestCheckResourceAttr("data.discord_members.test", "members.1000.username", "member999"),
				),
			},
			{
				Config: members("limit = 2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_members.test", "members.#", "2"),
					resource.TestCheckResourceAttr("data.discord_members.test", "members.1.user_id", ids[1]),
				),
			},
		},
	})
}

func TestAccBansDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	for i := range discord.MaxPageSize + 1 {
		env.fake.AddBan(env.serverID, strconv.Itoa(300000000000000000+i), "spam "+strconv.Itoa(i))
	}
	bans := func(attrs string) string {
		return env.config(`
data "discord_bans" "test" {
  server_id = local.server_id
  ` + attrs + `
}`)
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      bans("limit = -1"),
				ExpectError: regexp.MustCompile(`must be at least 1`),
			},
			{
				Config: bans(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_bans.test", "bans.#", "1001"),
					resource.TestCheckResourceAttr("data.discord_bans.test", "bans.0.user_id", "300000000000000000"),
					resource.TestCheckResourceAttr("data.discord_bans.test", "bans.0.reason", "spam 0"),
					resource.TestCheckResourceAttr("data.discord_bans.test", "bans.0.username", "user300000000000000000"),
					resource.TestCheckResourceAttr("data.discord_bans.test", "bans.1000.user_id", "300000000000001000"),
				),
			},
			{
				Config: bans("limit = 1001"),
				Check:  resource.TestCheckResourceAttr("data.discord_bans.test", "bans.#", "1001"),
			},
			{
				Config: bans("limit = 3"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_bans.test", "bans.#", "3"),
					resource.TestCheckResourceAttr("data.discord_bans.test", "bans.2.user_id", "300000000000000002"),
				),
			},
		},
	})
}

func TestAccEmojisDataSource(t *testing.T) {
	env := newTestEnv(t)
	cfg := env.config(`
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-emojis"
}
resource "discord_emoji" "test" {
  server_id = local.server_id
  name      = "tf_acc_emojis"
  image     = "` + onePixelPNG + `"
  roles     = [discord_role.test.id]
}
data "discord_emojis" "test" {
  server_id  = local.server_id
  depends_on = [discord_emoji.test]
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckTypeSetElemNestedAttrs("data.discord_emojis.test", "emojis.*", map[string]string{
					"name":     "tf_acc_emojis",
					"animated": "false",
					"managed":  "false",
					"roles.#":  "1",
				}),
				resource.TestCheckTypeSetElemAttrPair("data.discord_emojis.test", "emojis.*.id", "discord_emoji.test", "id"),
			),
		}},
	})
}

func TestAccStickersAndSoundsDataSources(t *testing.T) {
	env := newTestEnv(t)
	cfg := env.config(`
resource "discord_sticker" "test" {
  server_id   = local.server_id
  name        = "tf-acc-stickers"
  description = "Listed"
  tags        = "wave"
  file        = "` + stickerPNG + `"
}
resource "discord_soundboard_sound" "test" {
  server_id  = local.server_id
  name       = "tf-acc-sounds"
  sound      = "` + soundMP3 + `"
  volume     = 0.5
  emoji_name = "🦆"
}
data "discord_stickers" "test" {
  server_id  = local.server_id
  depends_on = [discord_sticker.test]
}
data "discord_soundboard_sounds" "test" {
  server_id  = local.server_id
  depends_on = [discord_soundboard_sound.test]
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckTypeSetElemNestedAttrs("data.discord_stickers.test", "stickers.*", map[string]string{
					"name":        "tf-acc-stickers",
					"description": "Listed",
					"tags":        "wave",
					"format_type": "png",
					"available":   "true",
				}),
				resource.TestCheckTypeSetElemAttrPair("data.discord_stickers.test", "stickers.*.id", "discord_sticker.test", "id"),
				resource.TestCheckTypeSetElemNestedAttrs("data.discord_soundboard_sounds.test", "sounds.*", map[string]string{
					"name":       "tf-acc-sounds",
					"volume":     "0.5",
					"emoji_name": "🦆",
					"available":  "true",
				}),
				resource.TestCheckTypeSetElemAttrPair("data.discord_soundboard_sounds.test", "sounds.*.id", "discord_soundboard_sound.test", "id"),
			),
		}},
	})
}

func TestAccWebhooksDataSource(t *testing.T) {
	env := newTestEnv(t)
	cfg := env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-webhooks"
}
resource "discord_webhook" "test" {
  channel_id = discord_text_channel.test.id
  name       = "tf-acc-webhooks"
}
resource "discord_text_channel" "other" {
  server_id = local.server_id
  name      = "tf-acc-webhooks-other"
}
resource "discord_webhook" "other" {
  channel_id = discord_text_channel.other.id
  name       = "tf-acc-webhooks-other"
}
data "discord_webhooks" "test" {
  server_id  = local.server_id
  depends_on = [discord_webhook.test, discord_webhook.other]
}
data "discord_webhooks" "channel" {
  server_id  = local.server_id
  channel_id = discord_text_channel.other.id
  depends_on = [discord_webhook.test, discord_webhook.other]
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckTypeSetElemNestedAttrs("data.discord_webhooks.test", "webhooks.*", map[string]string{
					"name": "tf-acc-webhooks",
					"type": "incoming",
				}),
				resource.TestCheckTypeSetElemAttrPair("data.discord_webhooks.test", "webhooks.*.id", "discord_webhook.test", "id"),
				resource.TestCheckTypeSetElemAttrPair("data.discord_webhooks.test", "webhooks.*.channel_id", "discord_text_channel.test", "id"),
				resource.TestCheckTypeSetElemAttrPair("data.discord_webhooks.test", "webhooks.*.id", "discord_webhook.other", "id"),
				resource.TestCheckResourceAttr("data.discord_webhooks.channel", "webhooks.#", "1"),
				resource.TestCheckResourceAttrPair("data.discord_webhooks.channel", "webhooks.0.id", "discord_webhook.other", "id"),
			),
		}},
	})
}

func TestAccInvitesDataSource(t *testing.T) {
	env := newTestEnv(t)
	cfg := env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-invites"
}
resource "discord_invite" "test" {
  channel_id = discord_text_channel.test.id
  max_age    = 0
  max_uses   = 5
}
data "discord_invites" "test" {
  server_id  = local.server_id
  depends_on = [discord_invite.test]
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckTypeSetElemNestedAttrs("data.discord_invites.test", "invites.*", map[string]string{
					"max_age":   "0",
					"max_uses":  "5",
					"temporary": "false",
				}),
				resource.TestCheckTypeSetElemAttrPair("data.discord_invites.test", "invites.*.code", "discord_invite.test", "id"),
				resource.TestCheckTypeSetElemAttrPair("data.discord_invites.test", "invites.*.channel_id", "discord_text_channel.test", "id"),
			),
		}},
	})
}

func TestAccScheduledEventsDataSource(t *testing.T) {
	env := newTestEnv(t)
	var eventID string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					e, err := c.CreateScheduledEvent(ctx, env.serverID, discord.Payload{
						"name":                 "tf-acc-events",
						"description":          "listed",
						"privacy_level":        discord.PrivacyLevelGuildOnly,
						"entity_type":          discord.ScheduledEventEntityExternal,
						"entity_metadata":      discord.Payload{"location": "Online"},
						"scheduled_start_time": eventTime(24 * time.Hour),
						"scheduled_end_time":   eventTime(25 * time.Hour),
					})
					if err == nil {
						eventID = e.ID
						t.Cleanup(func() { _ = c.DeleteScheduledEvent(context.Background(), env.serverID, eventID) })
					}
					return err
				}),
				Config: env.config(`
data "discord_scheduled_events" "test" {
  server_id = local.server_id
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					func(s *terraform.State) error {
						return resource.TestCheckTypeSetElemNestedAttrs("data.discord_scheduled_events.test", "scheduled_events.*", map[string]string{
							"id":          eventID,
							"name":        "tf-acc-events",
							"description": "listed",
							"entity_type": "external",
							"status":      "scheduled",
							"location":    "Online",
						})(s)
					},
					resource.TestCheckResourceAttrSet("data.discord_scheduled_events.test", "scheduled_events.0.scheduled_start_time"),
				),
			},
		},
	})
}

func TestAccAutoModerationRulesDataSource(t *testing.T) {
	env := newTestEnv(t)
	cfg := env.config(`
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-automod-list"
}
resource "discord_auto_moderation_rule" "test" {
  server_id    = local.server_id
  name         = "tf-acc-automod-list"
  event_type   = "message_send"
  trigger_type = "keyword"
  enabled      = true
  exempt_role_ids = [discord_role.test.id]
  trigger_metadata = {
    keyword_filter = ["free nitro"]
  }
  actions = [
    { type = "block_message" },
    { type = "timeout", duration_seconds = 60 },
  ]
}
data "discord_auto_moderation_rules" "test" {
  server_id  = local.server_id
  depends_on = [discord_auto_moderation_rule.test]
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckTypeSetElemNestedAttrs("data.discord_auto_moderation_rules.test", "rules.*", map[string]string{
					"name":                 "tf-acc-automod-list",
					"event_type":           "message_send",
					"trigger_type":         "keyword",
					"enabled":              "true",
					"action_types.#":       "2",
					"action_types.0":       "block_message",
					"action_types.1":       "timeout",
					"exempt_role_ids.#":    "1",
					"exempt_channel_ids.#": "0",
				}),
				resource.TestCheckTypeSetElemAttrPair("data.discord_auto_moderation_rules.test", "rules.*.id", "discord_auto_moderation_rule.test", "id"),
				resource.TestCheckTypeSetElemAttrPair("data.discord_auto_moderation_rules.test", "rules.*.exempt_role_ids.*", "discord_role.test", "id"),
			),
		}},
	})
}

func TestAccThreadsDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	parent := env.seedChannel("tf-acc-threads", discord.ChannelTypeText, 0, "")
	ctx := context.Background()
	start := func(name string) string {
		th, err := env.client.StartThread(ctx, parent, discord.Payload{"name": name, "type": discord.ChannelTypePublicThread})
		if err != nil {
			t.Fatal(err)
		}
		return th.ID
	}
	older := start("tf-acc-older")
	newer := start("tf-acc-newer")
	other := env.seedChannel("tf-acc-threads-other", discord.ChannelTypeText, 0, "")
	if _, err := env.client.StartThread(ctx, other, discord.Payload{"name": "tf-acc-other", "type": discord.ChannelTypePublicThread}); err != nil {
		t.Fatal(err)
	}
	cfg := env.config(`
data "discord_threads" "test" {
  server_id = local.server_id
}
data "discord_threads" "channel" {
  server_id  = local.server_id
  channel_id = "` + parent + `"
}`)
	archivedCfg := cfg + `
data "discord_threads" "public" {
  server_id  = local.server_id
  channel_id = "` + parent + `"
  archived   = "public"
}
data "discord_threads" "private" {
  server_id  = local.server_id
  channel_id = "` + parent + `"
  archived   = "private"
}`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
data "discord_threads" "test" {
  server_id = local.server_id
  archived  = "public"
}`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`channel_id`),
			},
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_threads.test", "threads.#", "3"),
					resource.TestCheckResourceAttr("data.discord_threads.channel", "threads.#", "2"),
					resource.TestCheckResourceAttr("data.discord_threads.channel", "threads.0.id", newer),
					resource.TestCheckResourceAttr("data.discord_threads.channel", "threads.1.id", older),
					resource.TestCheckResourceAttr("data.discord_threads.test", "threads.1.id", newer),
					resource.TestCheckResourceAttr("data.discord_threads.channel", "threads.0.name", "tf-acc-newer"),
					resource.TestCheckResourceAttr("data.discord_threads.channel", "threads.0.type", "public_thread"),
					resource.TestCheckResourceAttr("data.discord_threads.channel", "threads.0.channel_id", parent),
					resource.TestCheckResourceAttr("data.discord_threads.channel", "threads.0.locked", "false"),
					resource.TestCheckResourceAttr("data.discord_threads.test", "threads.2.id", older),
				),
			},
			{
				// Archived threads are not active, and are listed per channel
				// with archived.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyThread(ctx, newer, discord.Payload{"archived": true})
					return err
				}),
				Config: archivedCfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_threads.channel", "threads.#", "1"),
					resource.TestCheckResourceAttr("data.discord_threads.channel", "threads.0.id", older),
					resource.TestCheckResourceAttr("data.discord_threads.public", "threads.#", "1"),
					resource.TestCheckResourceAttr("data.discord_threads.public", "threads.0.id", newer),
					resource.TestCheckResourceAttr("data.discord_threads.private", "threads.#", "0"),
				),
			},
		},
	})
}

func TestAccIntegrationsDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	cfg := env.config(`
data "discord_integrations" "test" {
  server_id = local.server_id
}`)
	var id string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check:  resource.TestCheckResourceAttr("data.discord_integrations.test", "integrations.#", "0"),
			},
			{
				PreConfig: func() { id = env.fake.AddIntegration(env.serverID, "Example Bot", "discord") },
				Config:    cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.discord_integrations.test", "integrations.#", "1"),
					resource.TestCheckResourceAttrPtr("data.discord_integrations.test", "integrations.0.id", &id),
					resource.TestCheckResourceAttr("data.discord_integrations.test", "integrations.0.name", "Example Bot"),
					resource.TestCheckResourceAttr("data.discord_integrations.test", "integrations.0.type", "discord"),
					resource.TestCheckResourceAttr("data.discord_integrations.test", "integrations.0.enabled", "true"),
					resource.TestCheckResourceAttrSet("data.discord_integrations.test", "integrations.0.account_id"),
					resource.TestCheckResourceAttr("data.discord_integrations.test", "integrations.0.account_name", "Example Bot account"),
				),
			},
		},
	})
}

func TestAccServerTemplatesDataSource(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
resource "discord_server_template" "test" {
  server_id = local.server_id
  name      = "tf-acc-template"
}
data "discord_server_templates" "test" {
  server_id  = local.server_id
  depends_on = [discord_server_template.test]
}`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.discord_server_templates.test", "templates.#", "1"),
				resource.TestCheckResourceAttrPair("data.discord_server_templates.test", "templates.0.code", "discord_server_template.test", "code"),
				resource.TestCheckResourceAttr("data.discord_server_templates.test", "templates.0.name", "tf-acc-template"),
				resource.TestCheckNoResourceAttr("data.discord_server_templates.test", "templates.0.description"),
				resource.TestCheckResourceAttr("data.discord_server_templates.test", "templates.0.usage_count", "0"),
				resource.TestCheckResourceAttrPair("data.discord_server_templates.test", "templates.0.creator_id", "discord_server_template.test", "creator_id"),
				resource.TestCheckResourceAttr("data.discord_server_templates.test", "templates.0.is_dirty", "false"),
				resource.TestCheckResourceAttrPair("data.discord_server_templates.test", "templates.0.created_at", "discord_server_template.test", "created_at"),
				resource.TestCheckResourceAttrPair("data.discord_server_templates.test", "templates.0.updated_at", "discord_server_template.test", "updated_at"),
			),
		}},
	})
}

// TestAccListDataSourcesUnknownServer covers the API error of every list
// data source.
func TestAccListDataSourcesUnknownServer(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	names := []string{
		"channels", "roles", "members", "emojis", "stickers", "soundboard_sounds", "webhooks", "invites", "bans",
		"scheduled_events", "auto_moderation_rules", "threads", "integrations", "server_templates",
	}
	steps := make([]resource.TestStep, 0, len(names))
	for _, name := range slices.Sorted(slices.Values(names)) {
		steps = append(steps, resource.TestStep{
			Config: `
data "discord_` + name + `" "test" {
  server_id = "999999999999999999"
}`,
			ExpectError: regexp.MustCompile(`Unknown\s+Guild`),
		})
	}
	env.run(resource.TestCase{Steps: steps})
}
