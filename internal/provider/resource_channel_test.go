package provider

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

func importStep(name string, ignore ...string) resource.TestStep {
	return resource.TestStep{
		ResourceName:            name,
		ImportState:             true,
		ImportStateVerify:       true,
		ImportStateVerifyIgnore: ignore,
	}
}

func TestAccChannels(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_category_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-category"
}
resource "discord_text_channel" "test" {
  server_id           = local.server_id
  name                = "tf-acc-text"
  category_id         = discord_category_channel.test.id
  topic               = "Initial topic"
  rate_limit_per_user = 10
}
resource "discord_voice_channel" "test" {
  server_id   = local.server_id
  name        = "tf-acc-voice"
  category_id = discord_category_channel.test.id
  user_limit  = 5
}
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("discord_text_channel.test", "category_id", "discord_category_channel.test", "id"),
					resource.TestCheckResourceAttr("discord_text_channel.test", "topic", "Initial topic"),
					resource.TestCheckResourceAttr("discord_text_channel.test", "rate_limit_per_user", "10"),
					resource.TestCheckResourceAttr("discord_text_channel.test", "nsfw", "false"),
					resource.TestCheckResourceAttrSet("discord_text_channel.test", "default_auto_archive_duration"),
					resource.TestCheckResourceAttr("discord_voice_channel.test", "user_limit", "5"),
					resource.TestCheckResourceAttr("discord_voice_channel.test", "video_quality_mode", "auto"),
					resource.TestCheckResourceAttrSet("discord_voice_channel.test", "bitrate"),
				),
			},
			importStep("discord_category_channel.test"),
			importStep("discord_text_channel.test"),
			importStep("discord_voice_channel.test"),
			{
				// Removing optional attributes clears them, and moving out of
				// the category is an in-place update.
				Config: env.config(`
resource "discord_category_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-category"
}
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-text-renamed"
  nsfw      = true
}
resource "discord_voice_channel" "test" {
  server_id          = local.server_id
  name               = "tf-acc-voice"
  category_id        = discord_category_channel.test.id
  user_limit         = 5
  video_quality_mode = "full"
}
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_text_channel.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("discord_voice_channel.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("discord_category_channel.test", plancheck.ResourceActionNoop),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_text_channel.test", "name", "tf-acc-text-renamed"),
					resource.TestCheckNoResourceAttr("discord_text_channel.test", "category_id"),
					resource.TestCheckNoResourceAttr("discord_text_channel.test", "topic"),
					resource.TestCheckResourceAttr("discord_text_channel.test", "rate_limit_per_user", "0"),
					resource.TestCheckResourceAttr("discord_text_channel.test", "nsfw", "true"),
					resource.TestCheckResourceAttr("discord_voice_channel.test", "video_quality_mode", "full"),
				),
			},
		},
	})
}

func TestAccChannelDeletedOutsideTerraform(t *testing.T) {
	env := newTestEnv(t)
	var channelID string
	cfg := env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-deleted"
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check:  captureAttr("discord_text_channel.test", "id", &channelID),
			},
			{
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteChannel(ctx, channelID)
				}),
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_text_channel.test", plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

func TestAccChannelWrongTypeImport(t *testing.T) {
	env := newTestEnv(t)
	var channelID string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_category_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-wrong-type"
}`),
				Check: captureAttr("discord_category_channel.test", "id", &channelID),
			},
			{
				Config: env.config(`
resource "discord_category_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-wrong-type"
}
resource "discord_text_channel" "imported" {
  server_id = local.server_id
  name      = "tf-acc-wrong-type"
}`),
				ResourceName:      "discord_text_channel.imported",
				ImportState:       true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return channelID, nil },
				ExpectError:       regexp.MustCompile(`Unexpected channel type`),
			},
		},
	})
}

func TestAccAnnouncementAndStageChannels(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_announcement_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-news"
  topic     = "Announcements"
}
resource "discord_stage_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-stage"
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_announcement_channel.test", "topic", "Announcements"),
					resource.TestCheckResourceAttr("discord_stage_channel.test", "user_limit", "0"),
				),
			},
			importStep("discord_announcement_channel.test"),
			importStep("discord_stage_channel.test"),
		},
	})
}

func TestAccForumChannel(t *testing.T) {
	env := newTestEnv(t)
	sameTagID := statecheck.CompareValue(compare.ValuesSame())
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_forum_channel" "test" {
  server_id            = local.server_id
  name                 = "tf-acc-forum"
  topic                = "Be nice"
  require_tag          = true
  default_sort_order   = "creation_date"
  default_forum_layout = "gallery_view"
  default_reaction_emoji = {
    emoji_name = "👍"
  }
  available_tags = [
    { name = "question", emoji_name = "❓" },
    { name = "staff", moderated = true },
  ]
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_forum_channel.test", "require_tag", "true"),
					resource.TestCheckResourceAttr("discord_forum_channel.test", "default_sort_order", "creation_date"),
					resource.TestCheckResourceAttr("discord_forum_channel.test", "default_forum_layout", "gallery_view"),
					resource.TestCheckResourceAttr("discord_forum_channel.test", "default_reaction_emoji.emoji_name", "👍"),
					resource.TestCheckResourceAttr("discord_forum_channel.test", "available_tags.#", "2"),
					resource.TestCheckResourceAttr("discord_forum_channel.test", "available_tags.1.moderated", "true"),
					resource.TestCheckResourceAttrSet("discord_forum_channel.test", "available_tags.0.id"),
				),
				ConfigStateChecks: []statecheck.StateCheck{
					sameTagID.AddStateValue("discord_forum_channel.test", tfjsonpath.New("available_tags").AtSliceIndex(1).AtMapKey("id")),
				},
			},
			importStep("discord_forum_channel.test"),
			{
				// Reordering and adding tags keeps existing tag IDs.
				Config: env.config(`
resource "discord_forum_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-forum"
  available_tags = [
    { name = "staff", moderated = true },
    { name = "announcement" },
  ]
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_forum_channel.test", "available_tags.#", "2"),
					resource.TestCheckResourceAttr("discord_forum_channel.test", "require_tag", "false"),
					resource.TestCheckNoResourceAttr("discord_forum_channel.test", "default_sort_order"),
					resource.TestCheckNoResourceAttr("discord_forum_channel.test", "default_reaction_emoji"),
					resource.TestCheckNoResourceAttr("discord_forum_channel.test", "topic"),
				),
				ConfigStateChecks: []statecheck.StateCheck{
					sameTagID.AddStateValue("discord_forum_channel.test", tfjsonpath.New("available_tags").AtSliceIndex(0).AtMapKey("id")),
				},
			},
		},
	})
}

func TestAccForumChannelValidation(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_forum_channel" "test" {
  server_id              = local.server_id
  name                   = "x"
  default_reaction_emoji = {}
}`),
				ExpectError: regexp.MustCompile(`No attribute specified when one \(and only one\) of`),
			},
			{
				Config: env.config(`
resource "discord_forum_channel" "test" {
  server_id      = local.server_id
  name           = "x"
  available_tags = [{ name = "a", emoji_id = "123", emoji_name = "x" }]
}`),
				ExpectError: regexp.MustCompile(`cannot be specified when`),
			},
			{
				Config: env.config(`
resource "discord_text_channel" "test" {
  server_id                     = local.server_id
  name                          = "x"
  default_auto_archive_duration = 30
}`),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
		},
	})
}

func TestAccChannelPositions(t *testing.T) {
	env := newTestEnv(t)
	channels := `
resource "discord_category_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-positions"
}
resource "discord_text_channel" "a" {
  server_id   = local.server_id
  name        = "tf-acc-a"
  category_id = discord_category_channel.test.id
}
resource "discord_text_channel" "b" {
  server_id   = local.server_id
  name        = "tf-acc-b"
  category_id = discord_category_channel.test.id
}
`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(channels + `
resource "discord_channel_positions" "test" {
  server_id   = local.server_id
  channel_ids = [discord_text_channel.b.id, discord_text_channel.a.id]
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("discord_channel_positions.test", "channel_ids.0", "discord_text_channel.b", "id"),
					resource.TestCheckResourceAttrPair("discord_channel_positions.test", "channel_ids.1", "discord_text_channel.a", "id"),
				),
			},
			{
				Config: env.config(channels + `
resource "discord_channel_positions" "test" {
  server_id   = local.server_id
  channel_ids = [discord_text_channel.a.id, discord_text_channel.b.id]
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("discord_channel_positions.test", "channel_ids.0", "discord_text_channel.a", "id"),
					resource.TestCheckResourceAttrPair("discord_channel_positions.test", "channel_ids.1", "discord_text_channel.b", "id"),
				),
			},
		},
	})
}

func TestAccChannelPermission(t *testing.T) {
	env := newTestEnv(t)
	var channelID, roleID string
	base := `
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-perm"
}
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-perm"
}
`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(base + `
resource "discord_channel_permission" "test" {
  channel_id   = discord_text_channel.test.id
  overwrite_id = discord_role.test.id
  type         = "role"
  allow        = provider::discord::permissions(["VIEW_CHANNEL"])
  deny         = provider::discord::permissions(["SEND_MESSAGES"])
}
resource "discord_channel_permission" "everyone" {
  channel_id   = discord_text_channel.test.id
  overwrite_id = local.server_id
  type         = "role"
  deny         = provider::discord::permissions(["VIEW_CHANNEL"])
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_channel_permission.test", "allow", "1024"),
					resource.TestCheckResourceAttr("discord_channel_permission.test", "deny", "2048"),
					resource.TestCheckResourceAttr("discord_channel_permission.everyone", "allow", "0"),
					captureAttr("discord_text_channel.test", "id", &channelID),
					captureAttr("discord_role.test", "id", &roleID),
				),
			},
			importStep("discord_channel_permission.test"),
			{
				// Overwrite removed in the Discord client: recreated.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteChannelPermission(ctx, channelID, roleID)
				}),
				Config: env.config(base + `
resource "discord_channel_permission" "test" {
  channel_id   = discord_text_channel.test.id
  overwrite_id = discord_role.test.id
  type         = "role"
  allow        = provider::discord::permissions(["VIEW_CHANNEL", "SEND_MESSAGES"])
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_channel_permission.test", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("discord_channel_permission.everyone", plancheck.ResourceActionDestroy),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_channel_permission.test", "allow", "3072"),
					resource.TestCheckResourceAttr("discord_channel_permission.test", "deny", "0"),
				),
			},
			{
				// Allow/deny edited in the Discord client: updated in place.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.EditChannelPermission(ctx, channelID, discord.Overwrite{ID: roleID, Type: discord.OverwriteTypeRole, Allow: "0", Deny: "8"})
				}),
				Config: env.config(base + `
resource "discord_channel_permission" "test" {
  channel_id   = discord_text_channel.test.id
  overwrite_id = discord_role.test.id
  type         = "role"
  allow        = provider::discord::permissions(["VIEW_CHANNEL", "SEND_MESSAGES"])
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_channel_permission.test", plancheck.ResourceActionUpdate),
					},
				},
			},
		},
	})
}
