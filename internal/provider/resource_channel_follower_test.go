package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

const followerChannels = `
resource "discord_announcement_channel" "news" {
  server_id = local.server_id
  name      = "tf-acc-news"
}
resource "discord_text_channel" "target" {
  server_id = local.server_id
  name      = "tf-acc-follow-target"
}
resource "discord_text_channel" "other" {
  server_id = local.server_id
  name      = "tf-acc-follow-other"
}
`

func TestAccChannelFollower(t *testing.T) {
	env := newTestEnv(t)
	var webhookID, sourceID, otherID string
	cfg := env.config(followerChannels + `
resource "discord_channel_follower" "test" {
  source_channel_id = discord_announcement_channel.news.id
  channel_id        = discord_text_channel.target.id
  audit_log_reason  = "Follow the news"
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				// Only announcement channels can be followed.
				Config: env.config(followerChannels + `
resource "discord_channel_follower" "test" {
  source_channel_id = discord_text_channel.other.id
  channel_id        = discord_text_channel.target.id
}`),
				ExpectError: regexp.MustCompile(`Unable to follow channel`),
			},
			{
				Config: env.config(`
resource "discord_channel_follower" "test" {
  source_channel_id = "news"
  channel_id        = "1"
}`),
				ExpectError: regexp.MustCompile(`must be a Discord snowflake ID`),
			},
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_channel_follower.test", "id", &webhookID),
					captureAttr("discord_announcement_channel.news", "id", &sourceID),
					captureAttr("discord_text_channel.other", "id", &otherID),
					resource.TestCheckResourceAttrPair("discord_channel_follower.test", "source_channel_id", "discord_announcement_channel.news", "id"),
					resource.TestCheckResourceAttrPair("discord_channel_follower.test", "channel_id", "discord_text_channel.target", "id"),
					func(*terraform.State) error {
						w, err := env.client.GetFollowerWebhook(context.Background(), webhookID)
						if err != nil {
							return err
						}
						if w.Type != webhookTypeChannelFollower {
							return fmt.Errorf("webhook type = %d, want %d", w.Type, webhookTypeChannelFollower)
						}
						if env.fake != nil {
							h := env.fake.RequestHeaders("POST /channels/" + sourceID + "/followers")
							if len(h) == 0 || h[len(h)-1].Get("X-Audit-Log-Reason") != "Follow%20the%20news" {
								return errors.New("follow request did not send the audit log reason")
							}
						}
						return nil
					},
				),
			},
			importStep("discord_channel_follower.test", "audit_log_reason"),
			{
				// The webhook is moved to another channel in the Discord
				// client: followed again into the configured channel.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyWebhook(ctx, webhookID, discord.Payload{"channel_id": otherID})
					return err
				}),
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_channel_follower.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("discord_channel_follower.test", "channel_id", "discord_text_channel.target", "id"),
					captureAttr("discord_channel_follower.test", "id", &webhookID),
				),
			},
			{
				// The webhook is deleted in the Discord client, which
				// unfollows: followed again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteWebhook(ctx, webhookID)
				}),
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_channel_follower.test", plancheck.ResourceActionCreate)},
				},
			},
			{
				// Changing only the reason does not follow again.
				Config: env.config(followerChannels + `
resource "discord_channel_follower" "test" {
  source_channel_id = discord_announcement_channel.news.id
  channel_id        = discord_text_channel.target.id
  audit_log_reason  = "Still following"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_channel_follower.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr("discord_channel_follower.test", "audit_log_reason", "Still following"),
			},
		},
	})
}

// TestAccChannelFollowerLostSource keeps the followed channel in state when
// Discord stops returning it.
func TestAccChannelFollowerLostSource(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	var webhookID string
	cfg := env.config(followerChannels + `
resource "discord_channel_follower" "test" {
  source_channel_id = discord_announcement_channel.news.id
  channel_id        = discord_text_channel.target.id
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check:  captureAttr("discord_channel_follower.test", "id", &webhookID),
			},
			{
				PreConfig: func() { env.fake.LoseFollowSource(webhookID) },
				Config:    cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttrPair("discord_channel_follower.test", "source_channel_id", "discord_announcement_channel.news", "id"),
			},
		},
	})
}

func TestAccChannelFollowerImportOtherWebhook(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(followerChannels + `
resource "discord_webhook" "test" {
  channel_id = discord_text_channel.target.id
  name       = "tf-acc-incoming"
}`),
			},
			{
				ResourceName: "discord_channel_follower.test",
				ImportState:  true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return s.RootModule().Resources["discord_webhook.test"].Primary.ID, nil
				},
				Config: env.config(followerChannels + `
resource "discord_webhook" "test" {
  channel_id = discord_text_channel.target.id
  name       = "tf-acc-incoming"
}
resource "discord_channel_follower" "test" {
  source_channel_id = discord_announcement_channel.news.id
  channel_id        = discord_text_channel.target.id
}`),
				ExpectError: regexp.MustCompile(`not a Channel Follower webhook`),
			},
		},
	})
}
