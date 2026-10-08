package provider

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// onePixelPNG is a valid 1x1 PNG image.
const onePixelPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

func TestAccMemberRole(t *testing.T) {
	env := newTestEnv(t)
	env.requireUser()
	var roleID string
	cfg := env.config(`
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-member"
}
resource "discord_member_role" "test" {
  server_id = local.server_id
  user_id   = local.user_id
  role_id   = discord_role.test.id
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("discord_member_role.test", "role_id", "discord_role.test", "id"),
					captureAttr("discord_role.test", "id", &roleID),
				),
			},
			importStep("discord_member_role.test"),
			{
				// The role is revoked in the Discord client: granted again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.RemoveMemberRole(ctx, env.serverID, env.userID, roleID)
				}),
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_member_role.test", plancheck.ResourceActionCreate)},
				},
			},
			{
				Config: env.config(`
data "discord_member" "test" {
  server_id = local.server_id
  user_id   = local.user_id
}`) + `
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-member"
}
resource "discord_member_role" "test" {
  server_id = local.server_id
  user_id   = local.user_id
  role_id   = discord_role.test.id
}`,
				Check: resource.TestCheckTypeSetElemAttrPair("data.discord_member.test", "roles.*", "discord_role.test", "id"),
			},
		},
	})
}

func TestAccWebhook(t *testing.T) {
	env := newTestEnv(t)
	channels := `
resource "discord_text_channel" "a" {
  server_id = local.server_id
  name      = "tf-acc-hook-a"
}
resource "discord_text_channel" "b" {
  server_id = local.server_id
  name      = "tf-acc-hook-b"
}
`
	withAvatar := env.config(channels + `
resource "discord_webhook" "test" {
  channel_id = discord_text_channel.a.id
  name       = "tf-acc-hook"
  avatar     = "` + onePixelPNG + `"
}`)
	var id string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(channels + `
resource "discord_webhook" "test" {
  channel_id = discord_text_channel.b.id
  name       = "My Discord Hook"
}`),
				ExpectError: regexp.MustCompile(`must not contain "discord"`),
			},
			{
				Config: env.config(channels + `
resource "discord_webhook" "test" {
  channel_id = discord_text_channel.b.id
  name       = "hook"
  avatar     = "https://example.com/a.png"
}`),
				ExpectError: regexp.MustCompile(`base64 image data URI`),
			},
			{
				Config: withAvatar,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_webhook.test", "id", &id),
					resource.TestCheckResourceAttr("discord_webhook.test", "name", "tf-acc-hook"),
					resource.TestCheckResourceAttrSet("discord_webhook.test", "token"),
					resource.TestCheckResourceAttrSet("discord_webhook.test", "avatar_hash"),
					resource.TestMatchResourceAttr("discord_webhook.test", "url", regexp.MustCompile(`^https://discord\.com/api/webhooks/\d+/.+$`)),
					resource.TestCheckResourceAttr("discord_webhook.test", "server_id", env.serverID),
				),
			},
			importStep("discord_webhook.test", "avatar"),
			{
				// The avatar is removed in the Discord client: uploaded again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyWebhook(ctx, id, discord.Payload{"avatar": nil})
					return err
				}),
				Config: withAvatar,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_webhook.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttrSet("discord_webhook.test", "avatar_hash"),
			},
			{
				// Moving channels and removing the avatar are in-place updates
				// that keep the token.
				Config: env.config(channels + `
resource "discord_webhook" "test" {
  channel_id = discord_text_channel.b.id
  name       = "tf-acc-hook-renamed"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_webhook.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("discord_webhook.test", "channel_id", "discord_text_channel.b", "id"),
					resource.TestCheckResourceAttr("discord_webhook.test", "name", "tf-acc-hook-renamed"),
					resource.TestCheckNoResourceAttr("discord_webhook.test", "avatar_hash"),
					resource.TestCheckResourceAttrSet("discord_webhook.test", "token"),
				),
			},
		},
	})
}

func TestAccInvite(t *testing.T) {
	env := newTestEnv(t)
	var code string
	cfg := env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-invite"
}
resource "discord_invite" "test" {
  channel_id = discord_text_channel.test.id
  max_age    = 3600
  max_uses   = 5
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_invite.test", "max_age", "3600"),
					resource.TestCheckResourceAttr("discord_invite.test", "max_uses", "5"),
					resource.TestCheckResourceAttr("discord_invite.test", "unique", "true"),
					resource.TestCheckResourceAttrSet("discord_invite.test", "expires_at"),
					resource.TestMatchResourceAttr("discord_invite.test", "url", regexp.MustCompile(`^https://discord\.gg/.+`)),
					captureAttr("discord_invite.test", "code", &code),
				),
			},
			{
				ResourceName:      "discord_invite.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return s.RootModule().Resources["discord_text_channel.test"].Primary.ID + "/" + code, nil
				},
			},
			{
				// Revoked or expired invites are created again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteInvite(ctx, code)
				}),
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_invite.test", plancheck.ResourceActionCreate)},
				},
			},
			{
				Config: env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-invite"
}
resource "discord_invite" "test" {
  channel_id = discord_text_channel.test.id
  max_age    = 0
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_invite.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.TestCheckNoResourceAttr("discord_invite.test", "expires_at"),
			},
		},
	})
}

func TestAccMessage(t *testing.T) {
	env := newTestEnv(t)
	var channelID, messageID string
	channel := `
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-message"
}
`
	edited := env.config(channel + `
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
  content    = "Edited rules"
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(channel + `
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
}`),
				ExpectError: regexp.MustCompile(`At least one attribute out of`),
			},
			{
				Config: env.config(channel + `
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
  embeds     = [{ footer_icon_url = "https://example.com/i.png" }]
}`),
				ExpectError: regexp.MustCompile(`footer_text`),
			},
			{
				Config: env.config(channel + `
resource "discord_message" "test" {
  channel_id = discord_text_channel.test.id
  content    = "Welcome! Read the rules."
  pinned     = true
  embeds = [{
    title       = "Rules"
    description = "Be nice."
    color       = provider::discord::color("#57F287")
    footer_text = "Managed by Terraform"
    fields = [
      { name = "1", value = "No spam", inline = true },
      { name = "2", value = "No NSFW" },
    ]
  }]
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_message.test", "content", "Welcome! Read the rules."),
					resource.TestCheckResourceAttr("discord_message.test", "pinned", "true"),
					resource.TestCheckResourceAttr("discord_message.test", "embeds.0.title", "Rules"),
					resource.TestCheckResourceAttr("discord_message.test", "embeds.0.color", "5763719"),
					resource.TestCheckResourceAttr("discord_message.test", "embeds.0.fields.0.inline", "true"),
					resource.TestCheckResourceAttr("discord_message.test", "embeds.0.fields.1.inline", "false"),
					resource.TestCheckResourceAttrSet("discord_message.test", "author_id"),
					captureAttr("discord_text_channel.test", "id", &channelID),
					captureAttr("discord_message.test", "id", &messageID),
				),
			},
			{
				ResourceName:            "discord_message.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"allowed_mentions"},
				ImportStateIdFunc:       func(*terraform.State) (string, error) { return channelID + "/" + messageID, nil },
			},
			{
				Config: edited,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_message.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_message.test", "content", "Edited rules"),
					resource.TestCheckResourceAttr("discord_message.test", "pinned", "false"),
					resource.TestCheckNoResourceAttr("discord_message.test", "embeds"),
				),
			},
			{
				// Deleted by a moderator: posted again instead of failing the
				// refresh.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteMessage(ctx, channelID, messageID)
				}),
				Config: edited,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_message.test", plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

func TestAccEmoji(t *testing.T) {
	env := newTestEnv(t)
	role := `
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-emoji"
}
`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_emoji" "test" {
  server_id = local.server_id
  name      = "bad name"
  image     = "` + onePixelPNG + `"
}`),
				ExpectError: regexp.MustCompile(`2-32 letters`),
			},
			{
				Config: env.config(role + `
resource "discord_emoji" "test" {
  server_id = local.server_id
  name      = "tf_acc"
  image     = "` + onePixelPNG + `"
  roles     = [discord_role.test.id]
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_emoji.test", "name", "tf_acc"),
					resource.TestCheckResourceAttr("discord_emoji.test", "animated", "false"),
					resource.TestCheckResourceAttr("discord_emoji.test", "roles.#", "1"),
				),
			},
			{
				ResourceName:            "discord_emoji.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"image"},
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return env.serverID + "/" + s.RootModule().Resources["discord_emoji.test"].Primary.ID, nil
				},
			},
			{
				Config: env.config(role + `
resource "discord_emoji" "test" {
  server_id = local.server_id
  name      = "tf_acc_renamed"
  image     = "` + onePixelPNG + `"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_emoji.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_emoji.test", "name", "tf_acc_renamed"),
					resource.TestCheckNoResourceAttr("discord_emoji.test", "roles"),
				),
			},
		},
	})
}

func TestAccServerSettings(t *testing.T) {
	env := newTestEnv(t)
	guild, err := env.client.GetGuild(context.Background(), env.serverID)
	if err != nil {
		t.Fatal(err)
	}
	// Live runs only re-apply the server's current name, so nothing changes.
	name, afk := guild.Name, guild.AFKTimeout
	if !env.live {
		name, afk = "Renamed Server", 900
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_server_settings" "test" {
  server_id   = local.server_id
  afk_timeout = 45
}`),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
			{
				Config: env.config(`
resource "discord_server_settings" "test" {
  server_id   = local.server_id
  name        = "` + name + `"
  afk_timeout = ` + itoa(afk) + `
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_server_settings.test", "name", name),
					resource.TestCheckResourceAttr("discord_server_settings.test", "afk_timeout", itoa(afk)),
					resource.TestCheckResourceAttrSet("discord_server_settings.test", "owner_id"),
					resource.TestCheckResourceAttrSet("discord_server_settings.test", "verification_level"),
				),
			},
			{
				ResourceName:                         "discord_server_settings.test",
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateId:                        env.serverID,
				ImportStateVerifyIdentifierAttribute: "id",
			},
		},
	})
}

func TestAccServerSettingsUpdate(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_server_settings" "test" {
  server_id          = local.server_id
  verification_level = "high"
}`),
				Check: resource.TestCheckResourceAttr("discord_server_settings.test", "verification_level", "high"),
			},
			{
				Config: env.config(`
resource "discord_server_settings" "test" {
  server_id               = local.server_id
  verification_level      = "low"
  explicit_content_filter = "all_members"
  icon                    = "` + onePixelPNG + `"
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_server_settings.test", "verification_level", "low"),
					resource.TestCheckResourceAttr("discord_server_settings.test", "explicit_content_filter", "all_members"),
					resource.TestCheckResourceAttrSet("discord_server_settings.test", "icon_hash"),
					// Unmanaged settings keep their current value.
					resource.TestCheckResourceAttr("discord_server_settings.test", "name", "Test Server"),
				),
			},
		},
	})
}

func TestAccRateLimitedApply(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.fake.RateLimitNext("POST /guilds/"+env.serverID+"/roles", 2)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "rate-limited"
}`),
			Check: resource.TestCheckResourceAttr("discord_role.test", "name", "rate-limited"),
		}},
	})
}
