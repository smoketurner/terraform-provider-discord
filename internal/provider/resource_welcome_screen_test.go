package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

func TestAccWelcomeScreen(t *testing.T) {
	env := newTestEnv(t)
	welcome := "/guilds/" + env.serverID + "/welcome-screen"
	config := func(body string) string {
		return env.config(`
resource "discord_text_channel" "rules" {
  server_id = local.server_id
  name      = "tf-acc-welcome-rules"
}
resource "discord_text_channel" "chat" {
  server_id = local.server_id
  name      = "tf-acc-welcome-chat"
}
resource "discord_emoji" "test" {
  server_id = local.server_id
  name      = "tf_acc_welcome"
  image     = "` + onePixelPNG + `"
}
resource "discord_welcome_screen" "test" {
  server_id        = local.server_id
  audit_log_reason = "Welcome new members"
` + body + `
}`)
	}
	rules := `{
      channel_id  = discord_text_channel.rules.id
      description = "Read the rules"
      emoji_name  = "📜"
    }`
	chat := `{
      channel_id  = discord_text_channel.chat.id
      description = "Say hello"
      emoji_id    = discord_emoji.test.id
    }`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: config(`
  enabled     = true
  description = "Welcome to the server"
  welcome_channels = [` + rules + `, ` + chat + `]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_welcome_screen.test", "id", env.serverID),
					resource.TestCheckResourceAttr("discord_welcome_screen.test", "enabled", "true"),
					resource.TestCheckResourceAttr("discord_welcome_screen.test", "description", "Welcome to the server"),
					resource.TestCheckResourceAttr("discord_welcome_screen.test", "welcome_channels.#", "2"),
					resource.TestCheckResourceAttrPair("discord_welcome_screen.test", "welcome_channels.0.channel_id", "discord_text_channel.rules", "id"),
					resource.TestCheckResourceAttr("discord_welcome_screen.test", "welcome_channels.0.emoji_name", "📜"),
					resource.TestCheckNoResourceAttr("discord_welcome_screen.test", "welcome_channels.0.emoji_id"),
					resource.TestCheckResourceAttrPair("discord_welcome_screen.test", "welcome_channels.1.emoji_id", "discord_emoji.test", "id"),
					// Only the ID was configured, so the name Discord reads
					// back is not stored.
					resource.TestCheckNoResourceAttr("discord_welcome_screen.test", "welcome_channels.1.emoji_name"),
					func(*terraform.State) error {
						if env.live {
							return nil
						}
						return env.expectReasons(map[string]string{"PATCH " + welcome: "Welcome new members"})
					},
				),
			},
			{
				ResourceName:            "discord_welcome_screen.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"audit_log_reason"},
				ImportStateId:           env.serverID,
			},
			{
				// Reordering channels and removing the description.
				Config: config(`
  enabled          = true
  welcome_channels = [` + chat + `, ` + rules + `]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_welcome_screen.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("discord_welcome_screen.test", "description"),
					resource.TestCheckResourceAttrPair("discord_welcome_screen.test", "welcome_channels.0.channel_id", "discord_text_channel.chat", "id"),
					resource.TestCheckResourceAttrPair("discord_welcome_screen.test", "welcome_channels.1.channel_id", "discord_text_channel.rules", "id"),
				),
			},
			{
				// Drift: the welcome screen is disabled and emptied in the
				// Discord client.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyWelcomeScreen(ctx, env.serverID, discord.Payload{
						"enabled": false, "description": "Edited by hand", "welcome_channels": []any{},
					})
					return err
				}),
				Config: config(`
  enabled          = true
  welcome_channels = [` + chat + `, ` + rules + `]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_welcome_screen.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_welcome_screen.test", "enabled", "true"),
					resource.TestCheckNoResourceAttr("discord_welcome_screen.test", "description"),
					resource.TestCheckResourceAttr("discord_welcome_screen.test", "welcome_channels.#", "2"),
				),
			},
			{
				// Removing welcome_channels removes every channel.
				Config: config(`
  enabled     = true
  description = "Welcome to the server"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("discord_welcome_screen.test", "welcome_channels"),
					func(*terraform.State) error {
						ws, err := env.client.GetWelcomeScreen(context.Background(), env.serverID)
						if err != nil {
							return err
						}
						if len(ws.WelcomeChannels) != 0 {
							return fmt.Errorf("welcome screen still has %d channels", len(ws.WelcomeChannels))
						}
						return nil
					},
				),
			},
		},
		// Destroying disables the welcome screen and leaves its content.
		CheckDestroy: func(*terraform.State) error {
			g, err := env.client.GetGuild(context.Background(), env.serverID)
			if err != nil {
				return err
			}
			if slices.Contains(g.Features, featureWelcomeScreenEnabled) {
				return errors.New("welcome screen is still enabled after destroy")
			}
			ws, err := env.client.GetWelcomeScreen(context.Background(), env.serverID)
			if err != nil {
				return err
			}
			if ws.Description == nil || *ws.Description != "Welcome to the server" {
				return fmt.Errorf("welcome screen description = %v, want it unchanged", ws.Description)
			}
			return nil
		},
	})
}

// A server whose welcome screen was never set returns 404 for it; importing
// one reads as an empty, disabled welcome screen.
func TestAccWelcomeScreenNeverConfigured(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
import {
  to = discord_welcome_screen.test
  id = local.server_id
}
resource "discord_welcome_screen" "test" {
  server_id = local.server_id
  enabled   = false
}`),
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_welcome_screen.test", plancheck.ResourceActionNoop)},
			},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("discord_welcome_screen.test", "enabled", "false"),
				resource.TestCheckNoResourceAttr("discord_welcome_screen.test", "description"),
				resource.TestCheckNoResourceAttr("discord_welcome_screen.test", "welcome_channels"),
			),
		}},
	})
}

func TestAccWelcomeScreenValidation(t *testing.T) {
	env := newTestEnv(t)
	channel := `{
      channel_id  = "123456789012345678"
      description = "Channel"
    }`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_welcome_screen" "test" {
  server_id        = local.server_id
  enabled          = true
  welcome_channels = [` + strings.Repeat(channel+", ", 6) + `]
}`),
				ExpectError: regexp.MustCompile(`at most 5`),
			},
			{
				Config: env.config(`
resource "discord_welcome_screen" "test" {
  server_id   = local.server_id
  enabled     = true
  description = "` + strings.Repeat("x", 141) + `"
}`),
				ExpectError: regexp.MustCompile(`string length must be between 1 and\s+140`),
			},
			{
				Config: env.config(`
resource "discord_welcome_screen" "test" {
  server_id = local.server_id
  enabled   = true
  welcome_channels = [{
    channel_id  = "123456789012345678"
    description = "` + strings.Repeat("x", 51) + `"
  }]
}`),
				ExpectError: regexp.MustCompile(`description string length must be between 1 and\s+50`),
			},
		},
	})
}
