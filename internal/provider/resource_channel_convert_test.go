package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// Moving state between resource types needs Terraform 1.8.
var moveStateSupported = []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_8_0)}

// checkChannelType verifies the channel's type in Discord and that it is
// still the channel with the given ID.
func (e *testEnv) checkChannelType(name string, id *string, want int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("resource %s not found in state", name)
		}
		if rs.Primary.ID != *id {
			return fmt.Errorf("%s ID = %s, want %s: the channel was recreated", name, rs.Primary.ID, *id)
		}
		ch, err := e.client.GetChannel(context.Background(), *id)
		if err != nil {
			return err
		}
		if ch.Type != want {
			return fmt.Errorf("channel %s has type %d, want %d", *id, ch.Type, want)
		}
		return nil
	}
}

func expectConversion(name, to string) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate),
			plancheck.ExpectKnownValue(name, tfjsonpath.New("type"), knownvalue.StringExact(to)),
		},
	}
}

func TestAccChannelConvertTextAndAnnouncement(t *testing.T) {
	env := newTestEnv(t)
	var channelID string
	env.run(resource.TestCase{
		TerraformVersionChecks: moveStateSupported,
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_text_channel" "news" {
  server_id           = local.server_id
  name                = "tf-acc-convert"
  topic               = "Release notes"
  rate_limit_per_user = 10
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_text_channel.news", "id", &channelID),
					resource.TestCheckResourceAttr("discord_text_channel.news", "type", "text"),
				),
			},
			{
				Config: env.config(`
resource "discord_announcement_channel" "news" {
  server_id = local.server_id
  name      = "tf-acc-convert"
  topic     = "Announcements"
}
moved {
  from = discord_text_channel.news
  to   = discord_announcement_channel.news
}`),
				ConfigPlanChecks: expectConversion("discord_announcement_channel.news", "announcement"),
				Check: resource.ComposeAggregateTestCheckFunc(
					env.checkChannelType("discord_announcement_channel.news", &channelID, discord.ChannelTypeAnnouncement),
					resource.TestCheckResourceAttr("discord_announcement_channel.news", "type", "announcement"),
					resource.TestCheckResourceAttr("discord_announcement_channel.news", "topic", "Announcements"),
				),
			},
			importStep("discord_announcement_channel.news"),
			{
				Config: env.config(`
resource "discord_text_channel" "news" {
  server_id           = local.server_id
  name                = "tf-acc-convert"
  topic               = "Announcements"
  rate_limit_per_user = 30
}
moved {
  from = discord_announcement_channel.news
  to   = discord_text_channel.news
}`),
				ConfigPlanChecks: expectConversion("discord_text_channel.news", "text"),
				Check: resource.ComposeAggregateTestCheckFunc(
					env.checkChannelType("discord_text_channel.news", &channelID, discord.ChannelTypeText),
					resource.TestCheckResourceAttr("discord_text_channel.news", "type", "text"),
					resource.TestCheckResourceAttr("discord_text_channel.news", "rate_limit_per_user", "30"),
				),
			},
			importStep("discord_text_channel.news"),
		},
	})
}

func TestAccChannelConvertedOutsideTerraform(t *testing.T) {
	env := newTestEnv(t)
	var channelID string
	cfg := env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-converted"
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check:  captureAttr("discord_text_channel.test", "id", &channelID),
			},
			{
				// The channel is converted back rather than failing the
				// refresh or being recreated.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyChannel(ctx, channelID, discord.Payload{"type": discord.ChannelTypeAnnouncement})
					return err
				}),
				Config:           cfg,
				ConfigPlanChecks: expectConversion("discord_text_channel.test", "text"),
				Check:            env.checkChannelType("discord_text_channel.test", &channelID, discord.ChannelTypeText),
			},
		},
	})
}

func TestAccChannelConvertKeepsIdentity(t *testing.T) {
	env := newTestEnv(t)
	var channelID string
	env.run(resource.TestCase{
		TerraformVersionChecks: requiresIdentity,
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_text_channel" "news" {
  server_id = local.server_id
  name      = "tf-acc-convert-identity"
}`),
				Check:             captureAttr("discord_text_channel.news", "id", &channelID),
				ConfigStateChecks: expectIdentity("discord_text_channel.news", map[string]string{"channel_id": "id"}),
			},
			{
				Config: env.config(`
resource "discord_announcement_channel" "news" {
  server_id = local.server_id
  name      = "tf-acc-convert-identity"
}
moved {
  from = discord_text_channel.news
  to   = discord_announcement_channel.news
}`),
				ConfigPlanChecks:  expectConversion("discord_announcement_channel.news", "announcement"),
				Check:             env.checkChannelType("discord_announcement_channel.news", &channelID, discord.ChannelTypeAnnouncement),
				ConfigStateChecks: expectIdentity("discord_announcement_channel.news", map[string]string{"channel_id": "id"}),
			},
			identityImportStep("discord_announcement_channel.news"),
		},
	})
}

func TestAccChannelConvertRequiresNews(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		TerraformVersionChecks: moveStateSupported,
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_text_channel" "news" {
  server_id = local.server_id
  name      = "tf-acc-no-news"
}`),
			},
			{
				PreConfig: func() { env.fake.SetGuildFeatures() },
				Config: env.config(`
resource "discord_announcement_channel" "news" {
  server_id = local.server_id
  name      = "tf-acc-no-news"
}
moved {
  from = discord_text_channel.news
  to   = discord_announcement_channel.news
}`),
				ExpectError: regexp.MustCompile(`convert channel to announcement_channel`),
			},
		},
	})
}

func TestAccChannelMoveUnsupportedType(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		TerraformVersionChecks: moveStateSupported,
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_voice_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-move-voice"
}`),
			},
			{
				// Discord only converts between text and announcement channels.
				Config: env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-move-voice"
}
moved {
  from = discord_voice_channel.test
  to   = discord_text_channel.test
}`),
				ExpectError: regexp.MustCompile(`Unable to Move Resource State`),
			},
			{
				Config: env.config(`
resource "discord_voice_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-move-voice"
}`),
			},
		},
	})
}
