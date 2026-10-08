package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

const serverSettingsAddress = "discord_server_settings.test"

// checkGuild runs f on the server as Discord reports it.
func (e *testEnv) checkGuild(f func(g *discord.Guild) error) resource.TestCheckFunc {
	return func(*terraform.State) error {
		g, err := e.client.GetGuild(context.Background(), e.serverID)
		if err != nil {
			return err
		}
		return f(g)
	}
}

func expectFeatures(want map[string]bool) func(g *discord.Guild) error {
	return func(g *discord.Guild) error {
		for f, enabled := range want {
			if slices.Contains(g.Features, f) != enabled {
				return fmt.Errorf("feature %s enabled = %t, want %t (features %v)", f, !enabled, enabled, g.Features)
			}
		}
		return nil
	}
}

func TestAccServerSettingsFeatures(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	// VERIFIED is granted by Discord; the fake rejects requests that drop it.
	env.fake.SetGuildFeatures("COMMUNITY", "NEWS", "VERIFIED")
	channels := `
resource "discord_text_channel" "rules" {
  server_id = local.server_id
  name      = "rules"
}
resource "discord_text_channel" "updates" {
  server_id = local.server_id
  name      = "updates"
}
`
	settings := func(attrs string) string {
		return env.config(channels + `
resource "discord_server_settings" "test" {
  server_id = local.server_id
` + attrs + `
}`)
	}
	community := `  rules_channel_id          = discord_text_channel.rules.id
  public_updates_channel_id = discord_text_channel.updates.id
  community                 = true
`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      settings(`  community = true`),
				ExpectError: regexp.MustCompile(`(?s)rules_channel_id must be set to a channel when community is true`),
			},
			{
				Config: settings(`  community        = true
  rules_channel_id = ""
  public_updates_channel_id = discord_text_channel.updates.id`),
				ExpectError: regexp.MustCompile(`(?s)rules_channel_id must be set to a channel when community is true`),
			},
			{
				Config: settings(`  community = false`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(serverSettingsAddress, "community", "false"),
					resource.TestCheckResourceAttr(serverSettingsAddress, "invites_disabled", "false"),
					resource.TestCheckTypeSetElemAttr(serverSettingsAddress, "features.*", "VERIFIED"),
					env.checkGuild(expectFeatures(map[string]bool{"COMMUNITY": false, "NEWS": true, "VERIFIED": true})),
				),
			},
			{
				// Discord refuses Community without both channels.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyGuild(ctx, env.serverID, discord.Payload{"features": []string{"COMMUNITY", "NEWS", "VERIFIED"}})
					if err == nil {
						return errors.New("enabling COMMUNITY without channels succeeded")
					}
					return nil
				}),
				Config: settings(community + `  invites_disabled     = true
  raid_alerts_disabled = true`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(serverSettingsAddress, "community", "true"),
					resource.TestCheckResourceAttr(serverSettingsAddress, "invites_disabled", "true"),
					resource.TestCheckResourceAttr(serverSettingsAddress, "raid_alerts_disabled", "true"),
					resource.TestCheckResourceAttr(serverSettingsAddress, "discoverable", "false"),
					env.checkGuild(expectFeatures(map[string]bool{
						"COMMUNITY": true, "INVITES_DISABLED": true, "RAID_ALERTS_DISABLED": true, "NEWS": true, "VERIFIED": true,
					})),
				),
			},
			{
				// Invites are resumed in the Discord client: paused again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyGuild(ctx, env.serverID, discord.Payload{
						"features": []string{"COMMUNITY", "NEWS", "VERIFIED", "RAID_ALERTS_DISABLED"},
					})
					return err
				}),
				Config: settings(community + `  invites_disabled     = true
  raid_alerts_disabled = true`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(serverSettingsAddress, plancheck.ResourceActionUpdate)},
				},
				Check: env.checkGuild(expectFeatures(map[string]bool{"INVITES_DISABLED": true})),
			},
			{
				ResourceName:      serverSettingsAddress,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     env.serverID,
			},
			{
				// Unconfigured features are left as they are.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyGuild(ctx, env.serverID, discord.Payload{
						"features": []string{"COMMUNITY", "NEWS", "VERIFIED", "INVITES_DISABLED", "RAID_ALERTS_DISABLED", "DISCOVERABLE"},
					})
					return err
				}),
				Config: settings(community + `  invites_disabled = false`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(serverSettingsAddress, "discoverable", "true"),
					resource.TestCheckResourceAttr(serverSettingsAddress, "raid_alerts_disabled", "true"),
					env.checkGuild(expectFeatures(map[string]bool{
						"INVITES_DISABLED": false, "DISCOVERABLE": true, "RAID_ALERTS_DISABLED": true, "VERIFIED": true,
					})),
				),
			},
		},
	})
}

func TestAccServerSettingsClearChannels(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	channels := `
resource "discord_text_channel" "general" {
  server_id = local.server_id
  name      = "general"
}
resource "discord_voice_channel" "afk" {
  server_id = local.server_id
  name      = "afk"
}
`
	settings := func(attrs string) string {
		return env.config(channels + `
resource "discord_server_settings" "test" {
  server_id = local.server_id
` + attrs + `
}`)
	}
	cleared := settings(`  afk_channel_id           = ""
  system_channel_id        = ""
  safety_alerts_channel_id = ""
  description              = ""`)
	var general string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      settings(`  afk_channel_id = "afk"`),
				ExpectError: regexp.MustCompile(`must be a Discord snowflake ID, or "" to clear`),
			},
			{
				Config: settings(`  afk_channel_id    = discord_voice_channel.afk.id
  system_channel_id = discord_text_channel.general.id
  description       = "A server"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair(serverSettingsAddress, "afk_channel_id", "discord_voice_channel.afk", "id"),
					resource.TestCheckResourceAttrPair(serverSettingsAddress, "system_channel_id", "discord_text_channel.general", "id"),
					resource.TestCheckResourceAttr(serverSettingsAddress, "description", "A server"),
					captureAttr("discord_text_channel.general", "id", &general),
				),
			},
			{
				Config: cleared,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(serverSettingsAddress, tfjsonpath.New("afk_channel_id"), knownvalue.StringExact("")),
					statecheck.ExpectKnownValue(serverSettingsAddress, tfjsonpath.New("system_channel_id"), knownvalue.StringExact("")),
					statecheck.ExpectKnownValue(serverSettingsAddress, tfjsonpath.New("description"), knownvalue.StringExact("")),
				},
				Check: env.checkGuild(func(g *discord.Guild) error {
					if g.AFKChannelID != nil || g.SystemChannelID != nil || g.Description != nil {
						return fmt.Errorf("settings not cleared: afk %v, system %v, description %v", g.AFKChannelID, g.SystemChannelID, g.Description)
					}
					return nil
				}),
			},
			{
				// The system channel is set in the Discord client: cleared again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyGuild(ctx, env.serverID, discord.Payload{"system_channel_id": general})
					return err
				}),
				Config: cleared,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(serverSettingsAddress, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(serverSettingsAddress, tfjsonpath.New("system_channel_id"), knownvalue.StringExact("")),
					},
				},
				Check: env.checkGuild(func(g *discord.Guild) error {
					if g.SystemChannelID != nil {
						return fmt.Errorf("system channel is %s, want none", *g.SystemChannelID)
					}
					return nil
				}),
			},
			{
				// Imported settings have no "" to keep: Discord reports them as unset.
				ResourceName:            serverSettingsAddress,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateId:           env.serverID,
				ImportStateVerifyIgnore: []string{"afk_channel_id", "system_channel_id", "safety_alerts_channel_id", "description"},
			},
			{
				// Removing the arguments leaves the settings unmanaged.
				Config: settings(`  afk_channel_id = discord_voice_channel.afk.id`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(serverSettingsAddress, tfjsonpath.New("system_channel_id"), knownvalue.Null()),
				},
			},
		},
	})
}

// hashChanges returns, for each image, a state check that its hash differs
// from the one the previous check recorded.
func hashChanges(images []string) map[string]func() statecheck.StateCheck {
	out := make(map[string]func() statecheck.StateCheck, len(images))
	for _, name := range images {
		c := statecheck.CompareValue(compare.ValuesDiffer())
		out[name] = func() statecheck.StateCheck {
			return c.AddStateValue(serverSettingsAddress, tfjsonpath.New(name+"_hash"))
		}
	}
	return out
}

func TestAccServerSettingsImages(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	settings := func(image string) string {
		return env.config(`
resource "discord_server_settings" "test" {
  server_id        = local.server_id
  banner           = "` + image + `"
  splash           = "` + image + `"
  discovery_splash = "` + image + `"
}`)
	}
	images := []string{"banner", "splash", "discovery_splash"}
	comparers := hashChanges(images)
	record := func() []statecheck.StateCheck {
		var checks []statecheck.StateCheck
		for _, name := range images {
			checks = append(checks, comparers[name](),
				statecheck.ExpectKnownValue(serverSettingsAddress, tfjsonpath.New(name+"_hash"), knownvalue.NotNull()))
		}
		return checks
	}
	restored := statecheck.CompareValue(compare.ValuesSame())
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      settings("https://example.com/banner.png"),
				ExpectError: regexp.MustCompile(`base64 image data URI`),
			},
			{
				Config:            settings(onePixelPNG),
				ConfigStateChecks: record(),
			},
			{
				Config: settings(otherPNG),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(serverSettingsAddress, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: append(record(), restored.AddStateValue(serverSettingsAddress, tfjsonpath.New("banner_hash"))),
			},
			{
				// The banner is changed in the Discord client: uploaded again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyGuild(ctx, env.serverID, discord.Payload{"banner": onePixelPNG})
					return err
				}),
				Config: settings(otherPNG),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(serverSettingsAddress, plancheck.ResourceActionUpdate)},
				},
				// Uploading the configured banner again restores its hash.
				ConfigStateChecks: []statecheck.StateCheck{
					restored.AddStateValue(serverSettingsAddress, tfjsonpath.New("banner_hash")),
				},
			},
			{
				ResourceName:            serverSettingsAddress,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateId:           env.serverID,
				ImportStateVerifyIgnore: images,
			},
		},
	})
}

func TestAccServerSettingsWriteOnlyImages(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	settings := func(image, version string) string {
		return env.config(`
resource "discord_server_settings" "test" {
  server_id                   = local.server_id
  banner_wo                   = "` + image + `"
  banner_wo_version           = ` + version + `
  splash_wo                   = "` + image + `"
  splash_wo_version           = ` + version + `
  discovery_splash_wo         = "` + image + `"
  discovery_splash_wo_version = ` + version + `
}`)
	}
	images := []string{"banner", "splash", "discovery_splash"}
	comparers := hashChanges(images)
	record := func() []statecheck.StateCheck {
		var checks []statecheck.StateCheck
		for _, name := range images {
			checks = append(checks, comparers[name]())
			checks = append(checks, expectNull(serverSettingsAddress, name, name+"_wo")...)
		}
		return checks
	}
	restored := statecheck.CompareValue(compare.ValuesSame())
	env.run(resource.TestCase{
		TerraformVersionChecks: writeOnlySupported,
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_server_settings" "test" {
  server_id = local.server_id
  splash    = "` + onePixelPNG + `"
  splash_wo = "` + onePixelPNG + `"
  splash_wo_version = 1
}`),
				ExpectError: regexp.MustCompile(`(?s)Attribute "splash(_wo)?" cannot be specified when "splash(_wo)?" is\s+specified`),
			},
			{
				Config:            settings(onePixelPNG, "1"),
				ConfigStateChecks: record(),
			},
			{
				// A new value under the same version is not sent.
				Config: settings(otherPNG, "1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: settings(otherPNG, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(serverSettingsAddress, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: append(record(), restored.AddStateValue(serverSettingsAddress, tfjsonpath.New("discovery_splash_hash"))),
			},
			{
				// The discovery splash is changed in the Discord client: uploaded again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyGuild(ctx, env.serverID, discord.Payload{"discovery_splash": onePixelPNG})
					return err
				}),
				Config: settings(otherPNG, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(serverSettingsAddress, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(serverSettingsAddress, tfjsonpath.New("discovery_splash_wo_version"), knownvalue.Int64Exact(2)),
					// Uploading the configured splash again restores its hash.
					restored.AddStateValue(serverSettingsAddress, tfjsonpath.New("discovery_splash_hash")),
				},
			},
		},
	})
}
