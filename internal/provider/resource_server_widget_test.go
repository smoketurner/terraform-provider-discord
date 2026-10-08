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

func TestAccServerWidget(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	widget := "/guilds/" + env.serverID + "/widget"
	config := func(body string) string {
		return env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-widget"
}
resource "discord_server_widget" "test" {
  server_id        = local.server_id
  audit_log_reason = "Widget for the website"
` + body + `
}`)
	}
	var channelID string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_server_widget" "test" {
  server_id  = local.server_id
  enabled    = true
  channel_id = "general"
}`),
				ExpectError: regexp.MustCompile(`must be a Discord snowflake ID`),
			},
			{
				Config: config(`
  enabled    = true
  channel_id = discord_text_channel.test.id`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_server_widget.test", "id", env.serverID),
					resource.TestCheckResourceAttr("discord_server_widget.test", "enabled", "true"),
					resource.TestCheckResourceAttrPair("discord_server_widget.test", "channel_id", "discord_text_channel.test", "id"),
					captureAttr("discord_text_channel.test", "id", &channelID),
					func(*terraform.State) error {
						return env.expectReasons(map[string]string{"PATCH " + widget: "Widget for the website"})
					},
				),
			},
			{
				ResourceName:            "discord_server_widget.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"audit_log_reason"},
				ImportStateId:           env.serverID,
			},
			{
				// Removing channel_id clears the invite channel.
				Config: config(`
  enabled = true`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_server_widget.test", "enabled", "true"),
					resource.TestCheckNoResourceAttr("discord_server_widget.test", "channel_id"),
					func(*terraform.State) error {
						w, err := env.client.GetWidgetSettings(context.Background(), env.serverID)
						if err != nil {
							return err
						}
						if w.ChannelID != nil {
							return fmt.Errorf("widget channel = %s, want none", *w.ChannelID)
						}
						return nil
					},
				),
			},
			{
				// Drift: the widget is disabled and given a channel in the
				// Discord client.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyWidgetSettings(ctx, env.serverID, discord.Payload{"enabled": false, "channel_id": channelID})
					return err
				}),
				Config: config(`
  enabled = true`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_server_widget.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_server_widget.test", "enabled", "true"),
					resource.TestCheckNoResourceAttr("discord_server_widget.test", "channel_id"),
				),
			},
			{
				Config: config(`
  enabled    = true
  channel_id = discord_text_channel.test.id`),
			},
		},
		// Destroying disables the widget and leaves the channel in place.
		CheckDestroy: func(*terraform.State) error {
			w, err := env.client.GetWidgetSettings(context.Background(), env.serverID)
			if err != nil {
				return err
			}
			if w.Enabled {
				return errors.New("widget is still enabled after destroy")
			}
			return nil
		},
	})
}

func TestAccServerWidgetDisabled(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
resource "discord_server_widget" "test" {
  server_id = local.server_id
  enabled   = false
}`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("discord_server_widget.test", "enabled", "false"),
				resource.TestCheckNoResourceAttr("discord_server_widget.test", "channel_id"),
			),
		}},
	})
}
