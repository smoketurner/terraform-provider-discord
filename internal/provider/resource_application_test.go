package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
	"github.com/smoketurner/terraform-provider-discord/internal/discord/discordtest"
)

const (
	applicationSettingsAddress    = "discord_application_settings.test"
	roleConnectionMetadataAddress = "discord_application_role_connection_metadata.test"
	applicationEmojiAddress       = "discord_application_emoji.test"
)

// checkApplication runs f on the bot's application as Discord reports it.
func (e *testEnv) checkApplication(f func(a *discord.Application) error) resource.TestCheckFunc {
	return func(*terraform.State) error {
		a, err := e.client.GetCurrentApplication(context.Background())
		if err != nil {
			return err
		}
		return f(a)
	}
}

func expectApplicationFlags(set, unset int64) func(a *discord.Application) error {
	return func(a *discord.Application) error {
		if a.Flags&set != set || a.Flags&unset != 0 {
			return fmt.Errorf("flags = %d, want %d set and %d unset", a.Flags, set, unset)
		}
		return nil
	}
}

func TestAccApplicationSettings(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	// Discord sets this flag itself; changing the intents must keep it.
	const commandBadge = 1 << 23
	env.fake.UpdateApplication(func(a *discord.Application) { a.Flags = commandBadge })
	settings := func(attrs string) string {
		return `
resource "discord_application_settings" "test" {
` + attrs + `
}`
	}
	full := settings(`  description = "Test application"
  tags        = ["moderation", "utility"]
  integration_types_config = {
    guild_install = {
      scopes      = ["applications.commands", "bot"]
      permissions = "2048"
    }
    user_install = {}
  }
  install_params = {
    scopes      = ["bot"]
    permissions = "0"
  }
  custom_install_url                = "https://example.com/install"
  role_connections_verification_url = "https://example.com/verify"
  interactions_endpoint_url         = "https://example.com/interactions"
  event_webhooks_url                = "https://example.com/events"
  event_webhooks_status             = "enabled"
  event_webhooks_types              = ["APPLICATION_AUTHORIZED"]
  gateway_message_content_limited   = true
  icon                              = "` + onePixelPNG + `"
  cover_image                       = "` + onePixelPNG + `"`)
	var drifted string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      settings(`  tags = ["a", "b", "c", "d", "e", "f"]`),
				ExpectError: regexp.MustCompile(`set must contain at most 5 elements`),
			},
			{
				Config:      settings(`  tags = ["abcdefghijklmnopqrstu"]`),
				ExpectError: regexp.MustCompile(`(?s)character count must be\s+between 1 and 20`),
			},
			{
				Config:      settings(`  event_webhooks_status = "disabled_by_discord"`),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
			{
				Config:      settings(`  integration_types_config = { bogus = {} }`),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
			{
				Config:      settings(`  integration_types_config = { guild_install = { scopes = ["bot"] } }`),
				ExpectError: regexp.MustCompile(`(?s)permissions" must be\s+specified when`),
			},
			{
				Config:      settings(`  interactions_endpoint_url = "ftp://example.com"`),
				ExpectError: regexp.MustCompile(`must be an http or https URL`),
			},
			{
				Config: full,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(applicationSettingsAddress, "id", discordtest.ApplicationID),
					resource.TestCheckResourceAttr(applicationSettingsAddress, "name", "Test App"),
					resource.TestCheckResourceAttr(applicationSettingsAddress, "tags.#", "2"),
					resource.TestCheckResourceAttr(applicationSettingsAddress, "integration_types_config.guild_install.permissions", "2048"),
					resource.TestCheckNoResourceAttr(applicationSettingsAddress, "integration_types_config.user_install.scopes"),
					resource.TestCheckResourceAttr(applicationSettingsAddress, "gateway_message_content_limited", "true"),
					resource.TestCheckResourceAttr(applicationSettingsAddress, "gateway_presence_limited", "false"),
					resource.TestCheckResourceAttr(applicationSettingsAddress, "flags", strconv.Itoa(commandBadge|discord.ApplicationFlagGatewayMessageContentLimited)),
					resource.TestCheckResourceAttrSet(applicationSettingsAddress, "icon_hash"),
					resource.TestCheckResourceAttrSet(applicationSettingsAddress, "cover_image_hash"),
					env.checkApplication(func(a *discord.Application) error {
						cfg := a.IntegrationTypesConfig
						if a.Description != "Test application" || cfg[discord.IntegrationTypeUserInstall].OAuth2InstallParams != nil ||
							cfg[discord.IntegrationTypeGuildInstall].OAuth2InstallParams.Permissions != "2048" ||
							a.EventWebhooksStatus != discord.EventWebhooksEnabled || *a.RoleConnectionsVerificationURL != "https://example.com/verify" {
							return fmt.Errorf("unexpected application %+v", a)
						}
						return nil
					}),
				),
			},
			{
				// Changed in the Developer Portal: set again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					a, err := c.ModifyCurrentApplication(ctx, discord.Payload{
						"description": "changed", "flags": 0, "icon": otherPNG, "event_webhooks_url": nil,
						"integration_types_config": map[string]any{discord.IntegrationTypeGuildInstall: map[string]any{}},
					})
					if err == nil {
						drifted = *a.Icon
					}
					return err
				}),
				Config: full,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(applicationSettingsAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					attrDiffers(applicationSettingsAddress, "icon_hash", &drifted),
					resource.TestCheckResourceAttr(applicationSettingsAddress, "description", "Test application"),
					resource.TestCheckResourceAttr(applicationSettingsAddress, "event_webhooks_url", "https://example.com/events"),
					resource.TestCheckResourceAttr(applicationSettingsAddress, "integration_types_config.%", "2"),
					env.checkApplication(expectApplicationFlags(commandBadge|discord.ApplicationFlagGatewayMessageContentLimited, 0)),
				),
			},
			{
				ResourceName:            applicationSettingsAddress,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"icon", "cover_image", "install_params", "integration_types_config"},
			},
			{
				ResourceName:  applicationSettingsAddress,
				ImportState:   true,
				ImportStateId: "123456789012345678",
				ExpectError:   regexp.MustCompile(`token belongs to application`),
			},
			{
				// Clearing settings and switching intents.
				Config: settings(`  description                       = ""
  tags                              = []
  role_connections_verification_url = ""
  event_webhooks_status             = "disabled"
  gateway_message_content_limited   = false
  gateway_presence_limited          = true`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(applicationSettingsAddress, "role_connections_verification_url", ""),
					resource.TestCheckResourceAttr(applicationSettingsAddress, "tags.#", "0"),
					resource.TestCheckResourceAttr(applicationSettingsAddress, "event_webhooks_status", "disabled"),
					env.checkApplication(expectApplicationFlags(commandBadge|discord.ApplicationFlagGatewayPresenceLimited,
						discord.ApplicationFlagGatewayMessageContentLimited)),
					env.checkApplication(func(a *discord.Application) error {
						if a.RoleConnectionsVerificationURL != nil || a.Description != "" || len(a.Tags) != 0 {
							return fmt.Errorf("settings not cleared: %+v", a)
						}
						return nil
					}),
				),
			},
			{
				// Omitted settings are left unmanaged.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyCurrentApplication(ctx, discord.Payload{"tags": []string{"games"}, "flags": 0})
					return err
				}),
				Config: settings(`  description = ""`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr(applicationSettingsAddress, "gateway_presence_limited", "false"),
			},
		},
		// Destroying leaves the settings as they are.
		CheckDestroy: env.checkApplication(func(a *discord.Application) error {
			if !slices.Equal(a.Tags, []string{"games"}) {
				return fmt.Errorf("tags = %v after destroy", a.Tags)
			}
			return nil
		}),
	})
}

func TestAccApplicationSettingsWriteOnlyIcon(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	withIcon := func(image, version string) string {
		return `
resource "discord_application_settings" "test" {
  icon_wo         = "` + image + `"
  icon_wo_version = ` + version + `
}`
	}
	hashes := statecheck.CompareValue(compare.ValuesDiffer())
	var drifted string
	env.run(resource.TestCase{
		TerraformVersionChecks: writeOnlySupported,
		Steps: []resource.TestStep{
			{
				Config: withIcon(onePixelPNG, "1"),
				ConfigStateChecks: append(expectNull(applicationSettingsAddress, "icon_wo", "icon"),
					hashes.AddStateValue(applicationSettingsAddress, tfjsonpath.New("icon_hash")),
					statecheck.ExpectKnownValue(applicationSettingsAddress, tfjsonpath.New("icon_hash"), knownvalue.NotNull()),
				),
			},
			{
				Config: withIcon(otherPNG, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(applicationSettingsAddress, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{hashes.AddStateValue(applicationSettingsAddress, tfjsonpath.New("icon_hash"))},
			},
			{
				// The icon is changed in the Developer Portal: uploaded again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					a, err := c.ModifyCurrentApplication(ctx, discord.Payload{"icon": onePixelPNG})
					if err == nil {
						drifted = *a.Icon
					}
					return err
				}),
				Config: withIcon(otherPNG, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(applicationSettingsAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(applicationSettingsAddress, "icon_wo_version", "2"),
					attrDiffers(applicationSettingsAddress, "icon_hash", &drifted),
				),
			},
		},
	})
}

// expectRoleConnectionKeys checks the keys of the application's metadata
// records, in order.
func (e *testEnv) expectRoleConnectionKeys(keys ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		records, err := e.client.GetRoleConnectionMetadata(context.Background(), discordtest.ApplicationID)
		if err != nil {
			return err
		}
		got := []string{}
		for _, r := range records {
			got = append(got, r.Key)
		}
		if !slices.Equal(got, keys) {
			return fmt.Errorf("metadata keys = %v, want %v", got, keys)
		}
		return nil
	}
}

func TestAccRoleConnectionMetadata(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.fake.SetRoleConnectionMetadata([]discord.RoleConnectionMetadata{{Type: 7, Key: "existing", Name: "Existing", Description: "Replaced"}})
	metadata := func(records string) string {
		return `
resource "discord_application_role_connection_metadata" "test" {
  records = [` + records + `]
}`
	}
	record := func(typ, key string) string {
		return fmt.Sprintf(`{
    type        = %q
    key         = %q
    name        = "Name of %[2]s"
    description = "Description of %[2]s"
  },`, typ, key)
	}
	two := metadata(`{
    type               = "boolean_equal"
    key                = "member"
    name               = "Member"
    name_localizations = { fr = "Membre" }
    description        = "Has an account"
  },` + record("datetime_less_than_or_equal", "joined"))
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: metadata(record("boolean_equal", "a") + record("boolean_equal", "b") + record("boolean_equal", "c") +
					record("boolean_equal", "d") + record("boolean_equal", "e") + record("boolean_equal", "f")),
				ExpectError: regexp.MustCompile(`list must contain at most 5 elements`),
			},
			{
				Config:      metadata(record("boolean_equal", "Bad-Key")),
				ExpectError: regexp.MustCompile(`must be 1-50 characters of a-z, 0-9 and _`),
			},
			{
				Config:      metadata(record("boolean_equal", "same") + record("integer_equal", "same")),
				ExpectError: regexp.MustCompile(`(?s)same is used\s+more than once`),
			},
			{
				Config:      metadata(record("boolean_maybe", "a")),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
			{
				Config: `
resource "discord_application_role_connection_metadata" "test" {
  application_id = "123456789012345678"
  records        = []
}`,
				ExpectError: regexp.MustCompile(`(?s)Unknown\s+Application`),
			},
			{
				Config: two,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(roleConnectionMetadataAddress, "id", discordtest.ApplicationID),
					resource.TestCheckResourceAttr(roleConnectionMetadataAddress, "application_id", discordtest.ApplicationID),
					resource.TestCheckResourceAttr(roleConnectionMetadataAddress, "records.0.name_localizations.fr", "Membre"),
					resource.TestCheckNoResourceAttr(roleConnectionMetadataAddress, "records.1.name_localizations"),
					env.expectRoleConnectionKeys("member", "joined"),
				),
			},
			{
				// Replaced in the Developer Portal: set again.
				PreConfig: func() {
					env.fake.SetRoleConnectionMetadata([]discord.RoleConnectionMetadata{{Type: 3, Key: "level", Name: "Level", Description: "Level"}})
				},
				Config: two,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(roleConnectionMetadataAddress, plancheck.ResourceActionUpdate)},
				},
				Check: env.expectRoleConnectionKeys("member", "joined"),
			},
			{
				ResourceName:      roleConnectionMetadataAddress,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     discordtest.ApplicationID,
			},
			{
				Config: metadata(record("integer_greater_than_or_equal", "level")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(roleConnectionMetadataAddress, "records.#", "1"),
					resource.TestCheckResourceAttr(roleConnectionMetadataAddress, "records.0.type", "integer_greater_than_or_equal"),
					env.expectRoleConnectionKeys("level"),
				),
			},
		},
		CheckDestroy: env.expectRoleConnectionKeys(),
	})
}

func TestAccApplicationEmoji(t *testing.T) {
	env := newTestEnv(t)
	emoji := func(name, image string) string {
		return fmt.Sprintf(`
resource "discord_application_emoji" "test" {
  name  = %q
  image = %q
}`, name, image)
	}
	var id string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      emoji("a", onePixelPNG),
				ExpectError: regexp.MustCompile(`must be 2-32 letters, digits or underscores`),
			},
			{
				Config: emoji("tf_acc_app", onePixelPNG),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(applicationEmojiAddress, "application_id"),
					resource.TestCheckResourceAttr(applicationEmojiAddress, "animated", "false"),
					captureAttr(applicationEmojiAddress, "id", &id),
				),
			},
			{
				// Renaming keeps the emoji.
				Config: emoji("tf_acc_app_renamed", onePixelPNG),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(applicationEmojiAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttrPtr(applicationEmojiAddress, "id", &id),
			},
			{
				// A new image uploads a new emoji.
				Config: emoji("tf_acc_app_renamed", otherPNG),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(applicationEmojiAddress, plancheck.ResourceActionReplace)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					attrDiffers(applicationEmojiAddress, "id", &id),
					captureAttr(applicationEmojiAddress, "id", &id),
				),
			},
			{
				// Deleted outside Terraform: created again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					app, err := c.GetCurrentApplication(ctx)
					if err != nil {
						return err
					}
					return c.DeleteApplicationEmoji(ctx, app.ID, id)
				}),
				Config: emoji("tf_acc_app_renamed", otherPNG),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(applicationEmojiAddress, plancheck.ResourceActionCreate)},
				},
				Check: captureAttr(applicationEmojiAddress, "id", &id),
			},
			{
				ResourceName:            applicationEmojiAddress,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"image"},
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return s.RootModule().Resources[applicationEmojiAddress].Primary.Attributes["application_id"] + "/" + id, nil
				},
			},
		},
		CheckDestroy: func(*terraform.State) error {
			app, err := env.client.GetCurrentApplication(context.Background())
			if err != nil {
				return err
			}
			if _, err := env.client.GetApplicationEmoji(context.Background(), app.ID, id); !discord.IsNotFound(err) {
				return errors.New("application emoji still exists")
			}
			return nil
		},
	})
}

func TestAccApplicationEmojiWriteOnlyImage(t *testing.T) {
	env := newTestEnv(t)
	withImage := func(image, version string) string {
		return `
resource "discord_application_emoji" "test" {
  name             = "tf_acc_app_wo"
  image_wo         = "` + image + `"
  image_wo_version = ` + version + `
}`
	}
	var id string
	env.run(resource.TestCase{
		TerraformVersionChecks: writeOnlySupported,
		Steps: []resource.TestStep{
			{
				Config:            withImage(onePixelPNG, "1"),
				ConfigStateChecks: expectNull(applicationEmojiAddress, "image_wo", "image"),
				Check:             captureAttr(applicationEmojiAddress, "id", &id),
			},
			{
				Config: withImage(otherPNG, "1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: withImage(otherPNG, "2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(applicationEmojiAddress, plancheck.ResourceActionReplace)},
				},
				Check: attrDiffers(applicationEmojiAddress, "id", &id),
			},
		},
	})
}
