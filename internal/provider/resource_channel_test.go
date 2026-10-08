package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
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
				// Discord stores 10000 when 0 is sent to a stage channel.
				Config: env.config(`
resource "discord_stage_channel" "test" {
  server_id  = local.server_id
  name       = "tf-acc-stage"
  user_limit = 0
}`),
				ExpectError: regexp.MustCompile(`value must be between 1 and 10000`),
			},
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
					resource.TestCheckResourceAttr("discord_stage_channel.test", "user_limit", "10000"),
				),
			},
			importStep("discord_announcement_channel.test"),
			importStep("discord_stage_channel.test"),
			{
				Config: env.config(`
resource "discord_stage_channel" "test" {
  server_id  = local.server_id
  name       = "tf-acc-stage"
  user_limit = 50
}`),
				Check: resource.TestCheckResourceAttr("discord_stage_channel.test", "user_limit", "50"),
			},
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

func TestAccMediaChannel(t *testing.T) {
	env := newTestEnv(t)
	env.requireMediaChannels()
	sameTagID := statecheck.CompareValue(compare.ValuesSame())
	var channelID string
	updated := env.config(`
resource "discord_media_channel" "test" {
  server_id          = local.server_id
  name               = "tf-acc-media"
  default_sort_order = "latest_activity"
  available_tags = [
    { name = "video" },
    { name = "clip" },
  ]
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_category_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-media-category"
}
resource "discord_media_channel" "test" {
  server_id                          = local.server_id
  name                               = "tf-acc-media"
  category_id                        = discord_category_channel.test.id
  topic                              = "Share your builds"
  nsfw                               = true
  rate_limit_per_user                = 30
  default_thread_rate_limit_per_user = 60
  require_tag                        = true
  hide_media_download_options        = true
  default_sort_order                 = "creation_date"
  default_reaction_emoji = {
    emoji_name = "🔥"
  }
  available_tags = [
    { name = "screenshot", emoji_name = "📸" },
    { name = "video", moderated = true },
  ]
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_media_channel.test", "id", &channelID),
					resource.TestCheckResourceAttrPair("discord_media_channel.test", "category_id", "discord_category_channel.test", "id"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "topic", "Share your builds"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "nsfw", "true"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "rate_limit_per_user", "30"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "default_thread_rate_limit_per_user", "60"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "require_tag", "true"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "hide_media_download_options", "true"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "default_sort_order", "creation_date"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "default_reaction_emoji.emoji_name", "🔥"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "available_tags.#", "2"),
					resource.TestCheckResourceAttrSet("discord_media_channel.test", "default_auto_archive_duration"),
					resource.TestCheckNoResourceAttr("discord_media_channel.test", "default_forum_layout"),
					func(*terraform.State) error {
						ch, err := env.client.GetChannel(context.Background(), channelID)
						if err != nil {
							return err
						}
						if ch.Type != discord.ChannelTypeMedia || !ch.NSFW {
							return fmt.Errorf("type = %d, nsfw = %t; want %d, true", ch.Type, ch.NSFW, discord.ChannelTypeMedia)
						}
						return nil
					},
				),
				ConfigStateChecks: []statecheck.StateCheck{
					sameTagID.AddStateValue("discord_media_channel.test", tfjsonpath.New("available_tags").AtSliceIndex(1).AtMapKey("id")),
				},
			},
			importStep("discord_media_channel.test"),
			{
				// Removing optional attributes clears them, and reordering
				// tags keeps existing tag IDs.
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_media_channel.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("discord_media_channel.test", "category_id"),
					resource.TestCheckNoResourceAttr("discord_media_channel.test", "topic"),
					resource.TestCheckNoResourceAttr("discord_media_channel.test", "default_reaction_emoji"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "nsfw", "false"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "rate_limit_per_user", "0"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "require_tag", "false"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "hide_media_download_options", "false"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "default_sort_order", "latest_activity"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "available_tags.0.name", "video"),
				),
				ConfigStateChecks: []statecheck.StateCheck{
					sameTagID.AddStateValue("discord_media_channel.test", tfjsonpath.New("available_tags").AtSliceIndex(0).AtMapKey("id")),
				},
			},
			{
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyChannel(ctx, channelID, discord.Payload{
						"topic": "changed in Discord",
						"flags": discord.ChannelFlagHideMediaDownloadOptions,
					})
					return err
				}),
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_media_channel.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("discord_media_channel.test", "topic"),
					resource.TestCheckResourceAttr("discord_media_channel.test", "hide_media_download_options", "false"),
				),
			},
			{
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteChannel(ctx, channelID)
				}),
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_media_channel.test", plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

// Without Server Subscriptions Discord refuses to create a media channel.
func TestAccMediaChannelUnavailable(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
resource "discord_media_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-media"
}`),
			ExpectError: regexp.MustCompile(`Cannot\s+execute\s+action\s+on\s+this\s+channel\s+type\s+\(code\s+50024\)`),
		}},
	})
}

func TestAccMediaChannelValidation(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(fmt.Sprintf(`
resource "discord_media_channel" "test" {
  server_id = local.server_id
  name      = "x"
  topic     = %q
}`, strings.Repeat("a", 1025))),
				ExpectError: regexp.MustCompile(`string length must be between 1 and 1024`),
			},
			{
				Config: env.config(`
resource "discord_media_channel" "test" {
  server_id            = local.server_id
  name                 = "x"
  default_forum_layout = "gallery_view"
}`),
				ExpectError: regexp.MustCompile(`An argument named "default_forum_layout" is not expected here`),
			},
			{
				Config: env.config(`
resource "discord_media_channel" "test" {
  server_id      = local.server_id
  name           = "x"
  available_tags = [{ name = "a", emoji_id = "123", emoji_name = "x" }]
}`),
				ExpectError: regexp.MustCompile(`cannot be specified when`),
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

// seedChannel creates a channel directly through the API, so it is not managed
// by Terraform and its refresh cannot fail once the fake denies access to it.
func (e *testEnv) seedChannel(name string, typ int, position int64, parentID string) string {
	e.t.Helper()
	p := discord.Payload{"name": name, "type": typ, "position": position}
	if parentID != "" {
		p["parent_id"] = parentID
	}
	ch, err := e.client.CreateChannel(context.Background(), e.serverID, p)
	if err != nil {
		e.t.Fatalf("creating channel %s: %v", name, err)
	}
	return ch.ID
}

// checkChannelPositions verifies the channels have strictly ascending
// positions in the given order, and that each channel in fixed is at the given
// position.
func (e *testEnv) checkChannelPositions(order []string, fixed map[string]int64) resource.TestCheckFunc {
	return func(*terraform.State) error {
		ctx := context.Background()
		var prev int64 = -1
		for _, id := range order {
			ch, err := e.client.GetChannel(ctx, id)
			if err != nil {
				return err
			}
			if ch.Position <= prev {
				return fmt.Errorf("channel %s at position %d, want above %d", id, ch.Position, prev)
			}
			prev = ch.Position
		}
		for id, want := range fixed {
			ch, err := e.client.GetChannel(ctx, id)
			if err != nil {
				return err
			}
			if ch.Position != want {
				return fmt.Errorf("unlisted channel %s moved to position %d, want %d", id, ch.Position, want)
			}
		}
		return nil
	}
}

func TestAccChannelPositionsHidden(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	category := env.seedChannel("tf-acc-hidden", discord.ChannelTypeCategory, 0, "")
	a := env.seedChannel("tf-acc-a", discord.ChannelTypeText, 1, category)
	b := env.seedChannel("tf-acc-b", discord.ChannelTypeText, 2, category)
	unlisted := env.seedChannel("tf-acc-unlisted", discord.ChannelTypeText, 3, category)
	c := env.seedChannel("tf-acc-c", discord.ChannelTypeText, 4, category)
	denied := env.seedChannel("tf-acc-denied", discord.ChannelTypeText, 5, category)
	gone := env.seedChannel("tf-acc-gone", discord.ChannelTypeText, 6, category)
	env.fake.DenyChannel(denied)

	positions := func(ids ...string) string {
		list, _ := json.Marshal(ids)
		return env.config(fmt.Sprintf(`
resource "discord_channel_positions" "test" {
  server_id   = local.server_id
  channel_ids = %s
}`, list))
	}
	stateOrder := func(ids ...string) resource.TestCheckFunc {
		var checks []resource.TestCheckFunc
		for i, id := range ids {
			checks = append(checks, resource.TestCheckResourceAttr("discord_channel_positions.test", fmt.Sprintf("channel_ids.%d", i), id))
		}
		checks = append(checks, resource.TestCheckResourceAttr("discord_channel_positions.test", "channel_ids.#", strconv.Itoa(len(ids))))
		return resource.ComposeAggregateTestCheckFunc(checks...)
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: positions(a, b, c, gone),
				Check:  stateOrder(a, b, c, gone),
			},
			{
				// b disappears from the channel list but can still be fetched:
				// it keeps its slot in the ordering.
				PreConfig: func() { env.fake.HideChannel(b) },
				Config:    positions(c, b, a, gone),
				Check: resource.ComposeAggregateTestCheckFunc(
					stateOrder(c, b, a, gone),
					env.checkChannelPositions([]string{c, b, a, gone}, map[string]int64{unlisted: 3}),
				),
			},
			{
				Config: positions(c, b, a, gone),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config:      positions(c, b, a, gone, denied),
				ExpectError: regexp.MustCompile(`lacks\s+the\s+View\s+Channel\s+permission`),
			},
			{
				// A deleted channel drops out of state; applying it again fails.
				PreConfig: env.outsideTerraform(func(ctx context.Context, cl *discord.Client) error {
					return cl.DeleteChannel(ctx, gone)
				}),
				Config:      positions(c, b, a, gone),
				ExpectError: regexp.MustCompile(`(?s)Unknown Channel.*was\s+deleted`),
			},
			{
				Config: positions(c, b, a),
				Check:  stateOrder(c, b, a),
			},
			{
				// b exists but can no longer be read: refresh fails instead of
				// dropping it from state.
				PreConfig:   func() { env.fake.DenyChannel(b) },
				Config:      positions(c, b, a),
				ExpectError: regexp.MustCompile(`(?s)Unable to read channel.*View\s+Channel\s+permission`),
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
