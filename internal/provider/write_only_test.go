package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// otherPNG is a second valid 1x1 PNG, so tests can tell uploads apart.
const otherPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"

var writeOnlySupported = []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)}

// expectNull asserts that attributes are null in state.
func expectNull(address string, attrs ...string) []statecheck.StateCheck {
	checks := make([]statecheck.StateCheck, 0, len(attrs))
	for _, a := range attrs {
		checks = append(checks, statecheck.ExpectKnownValue(address, tfjsonpath.New(a), knownvalue.Null()))
	}
	return checks
}

// attrDiffers fails when a state attribute equals *other.
func attrDiffers(address, attr string, other *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[address]
		if !ok {
			return fmt.Errorf("resource %s not found in state", address)
		}
		if got := rs.Primary.Attributes[attr]; got == *other {
			return fmt.Errorf("%s.%s is still %q", address, attr, got)
		}
		return nil
	}
}

func TestAccWebhookWriteOnlyAvatar(t *testing.T) {
	env := newTestEnv(t)
	const address = "discord_webhook.test"
	webhook := func(attrs string) string {
		return env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-hook-wo"
}
resource "discord_webhook" "test" {
  channel_id = discord_text_channel.test.id
  name       = "tf-acc-hook"
` + attrs + `
}`)
	}
	withAvatar := func(image, version string) string {
		return webhook(`  avatar_wo         = "` + image + `"
  avatar_wo_version = ` + version)
	}
	hashes := statecheck.CompareValue(compare.ValuesDiffer())
	var id, drifted string
	env.run(resource.TestCase{
		TerraformVersionChecks: writeOnlySupported,
		Steps: []resource.TestStep{
			{
				Config: webhook(`  avatar            = "` + onePixelPNG + `"
  avatar_wo         = "` + onePixelPNG + `"
  avatar_wo_version = 1`),
				ExpectError: regexp.MustCompile(`(?s)Attribute "avatar(_wo)?" cannot be specified when "avatar(_wo)?" is\s+specified`),
			},
			{
				Config:      webhook(`  avatar_wo = "` + onePixelPNG + `"`),
				ExpectError: regexp.MustCompile(`(?s)Attribute "avatar_wo_version" must be specified when "avatar_wo" is\s+specified`),
			},
			{
				Config:      webhook(`  avatar_wo_version = 1`),
				ExpectError: regexp.MustCompile(`(?s)Attribute "avatar_wo" must be specified when "avatar_wo_version" is\s+specified`),
			},
			{
				Config:      withAvatar("https://example.com/a.png", "1"),
				ExpectError: regexp.MustCompile(`base64 image data URI`),
			},
			{
				Config: withAvatar(onePixelPNG, "1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "avatar_wo_version", "1"),
					captureAttr(address, "id", &id),
				),
				ConfigStateChecks: append(expectNull(address, "avatar_wo", "avatar"),
					hashes.AddStateValue(address, tfjsonpath.New("avatar_hash")),
					statecheck.ExpectKnownValue(address, tfjsonpath.New("avatar_hash"), knownvalue.NotNull()),
				),
			},
			{
				// A new value under the same version is not sent.
				Config: withAvatar(otherPNG, "1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: withAvatar(otherPNG, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: append(expectNull(address, "avatar_wo"),
					hashes.AddStateValue(address, tfjsonpath.New("avatar_hash"))),
			},
			{
				// The avatar is changed in the Discord client: uploaded again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					w, err := c.ModifyWebhook(ctx, id, discord.Payload{"avatar": onePixelPNG})
					if err == nil {
						drifted = *w.Avatar
					}
					return err
				}),
				Config: withAvatar(otherPNG, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "avatar_wo_version", "2"),
					attrDiffers(address, "avatar_hash", &drifted),
				),
				ConfigStateChecks: expectNull(address, "avatar_wo"),
			},
			{
				ResourceName:            address,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"avatar_wo_version", "store_secrets", "token", "url"},
			},
			{
				// Switching to the stored argument uploads it.
				Config: webhook(`  avatar = "` + onePixelPNG + `"`),
				ConfigStateChecks: []statecheck.StateCheck{
					hashes.AddStateValue(address, tfjsonpath.New("avatar_hash")),
					statecheck.ExpectKnownValue(address, tfjsonpath.New("avatar"), knownvalue.StringExact(onePixelPNG)),
				},
			},
			{
				Config: withAvatar(otherPNG, "1"),
				ConfigStateChecks: append(expectNull(address, "avatar_wo", "avatar"),
					hashes.AddStateValue(address, tfjsonpath.New("avatar_hash"))),
			},
			{
				// Removing the version removes the avatar.
				Config:            webhook(""),
				ConfigStateChecks: expectNull(address, "avatar_hash", "avatar_wo_version"),
			},
		},
	})
}

func TestAccServerSettingsWriteOnlyIcon(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	const address = "discord_server_settings.test"
	settings := func(attrs string) string {
		return env.config(`
resource "discord_server_settings" "test" {
  server_id = local.server_id
` + attrs + `
}`)
	}
	withIcon := func(image, version string) string {
		return settings(`  icon_wo         = "` + image + `"
  icon_wo_version = ` + version)
	}
	hashes := statecheck.CompareValue(compare.ValuesDiffer())
	var drifted, kept string
	env.run(resource.TestCase{
		TerraformVersionChecks: writeOnlySupported,
		Steps: []resource.TestStep{
			{
				Config: settings(`  icon            = "` + onePixelPNG + `"
  icon_wo         = "` + onePixelPNG + `"
  icon_wo_version = 1`),
				ExpectError: regexp.MustCompile(`(?s)Attribute "icon(_wo)?" cannot be specified when "icon(_wo)?" is\s+specified`),
			},
			{
				Config: withIcon(onePixelPNG, "1"),
				ConfigStateChecks: append(expectNull(address, "icon_wo", "icon"),
					hashes.AddStateValue(address, tfjsonpath.New("icon_hash")),
					statecheck.ExpectKnownValue(address, tfjsonpath.New("icon_hash"), knownvalue.NotNull()),
				),
			},
			{
				Config: withIcon(otherPNG, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: append(expectNull(address, "icon_wo"),
					hashes.AddStateValue(address, tfjsonpath.New("icon_hash"))),
			},
			{
				// The icon is changed in the Discord client: uploaded again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					g, err := c.ModifyGuild(ctx, env.serverID, discord.Payload{"icon": onePixelPNG})
					if err == nil {
						drifted = *g.Icon
					}
					return err
				}),
				Config: withIcon(otherPNG, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "icon_wo_version", "2"),
					attrDiffers(address, "icon_hash", &drifted),
					captureAttr(address, "icon_hash", &kept),
				),
			},
			{
				// Removing the version leaves the icon in place.
				Config:            settings(`  name = "Test Server"`),
				Check:             resource.TestCheckResourceAttrPtr(address, "icon_hash", &kept),
				ConfigStateChecks: expectNull(address, "icon_wo_version"),
			},
		},
	})
}

func TestAccRoleWriteOnlyIcon(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.fake.SetGuildFeatures("COMMUNITY", "ROLE_ICONS")
	const address = "discord_role.test"
	role := func(attrs string) string {
		return env.config(`
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-role-wo"
` + attrs + `
}`)
	}
	withIcon := func(image, version string) string {
		return role(`  icon_wo         = "` + image + `"
  icon_wo_version = ` + version)
	}
	hashes := statecheck.CompareValue(compare.ValuesDiffer())
	var id, drifted string
	env.run(resource.TestCase{
		TerraformVersionChecks: writeOnlySupported,
		Steps: []resource.TestStep{
			{
				Config: role(`  icon            = "` + onePixelPNG + `"
  icon_wo         = "` + onePixelPNG + `"
  icon_wo_version = 1`),
				ExpectError: regexp.MustCompile(`(?s)Attribute "icon(_wo)?" cannot be specified when "icon(_wo)?" is\s+specified`),
			},
			{
				Config:      role(`  icon_wo = "` + onePixelPNG + `"`),
				ExpectError: regexp.MustCompile(`(?s)Attribute "icon_wo_version" must be specified when "icon_wo" is\s+specified`),
			},
			{
				Config:      withIcon("https://example.com/a.png", "1"),
				ExpectError: regexp.MustCompile(`base64 image data URI`),
			},
			{
				Config: withIcon(onePixelPNG, "1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "icon_wo_version", "1"),
					captureAttr(address, "id", &id),
				),
				ConfigStateChecks: append(expectNull(address, "icon_wo", "icon"),
					hashes.AddStateValue(address, tfjsonpath.New("icon_hash")),
					statecheck.ExpectKnownValue(address, tfjsonpath.New("icon_hash"), knownvalue.NotNull()),
				),
			},
			{
				// A new value under the same version is not sent.
				Config: withIcon(otherPNG, "1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: withIcon(otherPNG, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: append(expectNull(address, "icon_wo"),
					hashes.AddStateValue(address, tfjsonpath.New("icon_hash"))),
			},
			{
				// The icon is changed in the Discord client: uploaded again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					r, err := c.ModifyRole(ctx, env.serverID, id, discord.Payload{"icon": onePixelPNG})
					if err == nil {
						drifted = *r.Icon
					}
					return err
				}),
				Config: withIcon(otherPNG, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "icon_wo_version", "2"),
					attrDiffers(address, "icon_hash", &drifted),
				),
				ConfigStateChecks: expectNull(address, "icon_wo"),
			},
			{
				ResourceName:            address,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"icon_wo_version"},
				ImportStateIdFunc:       func(*terraform.State) (string, error) { return env.serverID + "/" + id, nil },
			},
			{
				// Switching to the stored argument uploads it.
				Config: role(`  icon = "` + onePixelPNG + `"`),
				ConfigStateChecks: []statecheck.StateCheck{
					hashes.AddStateValue(address, tfjsonpath.New("icon_hash")),
					statecheck.ExpectKnownValue(address, tfjsonpath.New("icon"), knownvalue.StringExact(onePixelPNG)),
				},
			},
			{
				Config: withIcon(otherPNG, "1"),
				ConfigStateChecks: append(expectNull(address, "icon_wo", "icon"),
					hashes.AddStateValue(address, tfjsonpath.New("icon_hash"))),
			},
			{
				// Removing the version removes the icon.
				Config:            role(""),
				ConfigStateChecks: expectNull(address, "icon_hash", "icon_wo_version"),
			},
		},
	})
}

func TestAccEmojiWriteOnlyImage(t *testing.T) {
	env := newTestEnv(t)
	const address = "discord_emoji.test"
	emoji := func(attrs string) string {
		return env.config(`
resource "discord_emoji" "test" {
  server_id = local.server_id
  name      = "tf_acc_wo"
` + attrs + `
}`)
	}
	withImage := func(version string) string {
		return emoji(`  image_wo         = "` + onePixelPNG + `"
  image_wo_version = ` + version)
	}
	ids := statecheck.CompareValue(compare.ValuesDiffer())
	env.run(resource.TestCase{
		TerraformVersionChecks: writeOnlySupported,
		Steps: []resource.TestStep{
			{
				Config:      emoji(""),
				ExpectError: regexp.MustCompile(`No attribute specified when one \(and only one\) of`),
			},
			{
				Config: emoji(`  image            = "` + onePixelPNG + `"
  image_wo         = "` + onePixelPNG + `"
  image_wo_version = 1`),
				ExpectError: regexp.MustCompile(`2 attributes specified when one \(and only one\) of`),
			},
			{
				Config: withImage("1"),
				ConfigStateChecks: append(expectNull(address, "image_wo", "image"),
					ids.AddStateValue(address, tfjsonpath.New("id"))),
			},
			{
				ResourceName:            address,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"image_wo_version"},
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return env.serverID + "/" + s.RootModule().Resources[address].Primary.ID, nil
				},
			},
			{
				// The image of an emoji cannot change: a new version uploads a new emoji.
				Config: withImage("2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionReplace)},
				},
				ConfigStateChecks: append(expectNull(address, "image_wo"),
					ids.AddStateValue(address, tfjsonpath.New("id"))),
			},
			{
				// Switching to the stored argument keeps the emoji.
				Config: emoji(`  image = "` + onePixelPNG + `"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
			},
			{
				Config: withImage("3"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
			},
		},
	})
}

// Write-only arguments need Terraform 1.11; older versions reject them.
func TestAccWriteOnlyUnsupported(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{tfversion.SkipAbove(tfversion.Version1_10_0)},
		Steps: []resource.TestStep{{
			Config: env.config(`
resource "discord_emoji" "test" {
  server_id        = local.server_id
  name             = "tf_acc_wo"
  image_wo         = "` + onePixelPNG + `"
  image_wo_version = 1
}`),
			ExpectError: regexp.MustCompile(`Write-only attributes are only supported in Terraform 1.11`),
		}},
	})
}
