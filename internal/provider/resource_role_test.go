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

func TestAccRole(t *testing.T) {
	env := newTestEnv(t)
	var roleID string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_role" "test" {
  server_id   = local.server_id
  name        = "tf-acc-role"
  permissions = provider::discord::permissions(["VIEW_CHANNEL", "SEND_MESSAGES"])
  color       = provider::discord::color("#5865F2")
  hoist       = true
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_role.test", "name", "tf-acc-role"),
					resource.TestCheckResourceAttr("discord_role.test", "permissions", "3072"),
					resource.TestCheckResourceAttr("discord_role.test", "color", "5793266"),
					resource.TestCheckResourceAttr("discord_role.test", "hoist", "true"),
					resource.TestCheckResourceAttr("discord_role.test", "mentionable", "false"),
					resource.TestCheckResourceAttr("discord_role.test", "managed", "false"),
					resource.TestCheckResourceAttrSet("discord_role.test", "position"),
					captureAttr("discord_role.test", "id", &roleID),
				),
			},
			{
				ResourceName:      "discord_role.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return env.serverID + "/" + roleID, nil },
			},
			{
				Config: env.config(`
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-role-renamed"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_role.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_role.test", "name", "tf-acc-role-renamed"),
					resource.TestCheckResourceAttr("discord_role.test", "permissions", "0"),
					resource.TestCheckResourceAttr("discord_role.test", "color", "0"),
					resource.TestCheckResourceAttr("discord_role.test", "hoist", "false"),
				),
			},
			{
				// Drift: the role is edited in the Discord client.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyRole(ctx, env.serverID, roleID, discord.Payload{"name": "edited-by-hand"})
					return err
				}),
				Config: env.config(`
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-role-renamed"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_role.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr("discord_role.test", "name", "tf-acc-role-renamed"),
			},
			{
				// Deleted outside Terraform: recreated rather than erroring.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteRole(ctx, env.serverID, roleID)
				}),
				Config: env.config(`
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-role-renamed"
}`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_role.test", plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

func TestAccRoleIcon(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	const address = "discord_role.test"
	role := func(attrs string) string {
		return env.config(`
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-role-icon"
` + attrs + `
}`)
	}
	withIcon := role(`  icon = "` + onePixelPNG + `"`)
	var id, hash string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      role(`  icon = "https://example.com/a.png"`),
				ExpectError: regexp.MustCompile(`base64 image data URI`),
			},
			{
				Config:      withIcon,
				ExpectError: regexp.MustCompile(`needs more boosts`),
			},
			{
				PreConfig: func() { env.fake.SetGuildFeatures("COMMUNITY", "ROLE_ICONS") },
				Config:    withIcon,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "icon", onePixelPNG),
					resource.TestCheckResourceAttrSet(address, "icon_hash"),
					captureAttr(address, "id", &id),
					captureAttr(address, "icon_hash", &hash),
				),
			},
			{
				ResourceName:            address,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"icon"},
				ImportStateIdFunc:       func(*terraform.State) (string, error) { return env.serverID + "/" + id, nil },
			},
			{
				// The icon is removed in the Discord client: uploaded again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyRole(ctx, env.serverID, id, discord.Payload{"icon": nil})
					return err
				}),
				Config: withIcon,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(address, "icon_hash"),
					attrDiffers(address, "icon_hash", &hash),
				),
			},
			{
				// Removing the argument removes the icon.
				Config: role(""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(address, "icon"),
					resource.TestCheckNoResourceAttr(address, "icon_hash"),
				),
			},
		},
	})
}

func TestAccRoleValidation(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`resource "discord_role" "test" {
  server_id = local.server_id
  name = "x"
  permissions = "-1"
}`),
				ExpectError: regexp.MustCompile(`decimal permission bitfield`),
			},
			{
				Config: env.config(`resource "discord_role" "test" {
  server_id = local.server_id
  name = "x"
  tertiary_color = 1
}`),
				ExpectError: regexp.MustCompile(`secondary_color`),
			},
			{
				Config: env.config(`resource "discord_role" "test" {
  server_id = "abc"
  name = "x"
}`),
				ExpectError: regexp.MustCompile(`snowflake`),
			},
		},
	})
}

func TestAccRoleImportInvalidID(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`resource "discord_role" "test" {
  server_id = local.server_id
  name = "x"
}`),
			ResourceName:  "discord_role.test",
			ImportState:   true,
			ImportStateId: "only-one-part",
			ExpectError:   regexp.MustCompile(`server_id/role_id`),
		}},
	})
}

func TestAccRoleEveryone(t *testing.T) {
	env := newTestEnv(t)
	role, err := env.client.GetRole(context.Background(), env.serverID, env.serverID)
	if err != nil {
		t.Fatal(err)
	}
	// Destroying discord_role_everyone leaves permissions as they are, so put
	// back what the server had before the test.
	t.Cleanup(func() {
		if _, err := env.client.ModifyRole(context.Background(), env.serverID, env.serverID, discord.Payload{"permissions": role.Permissions}); err != nil {
			t.Errorf("restoring @everyone permissions: %v", err)
		}
	})
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
resource "discord_role_everyone" "test" {
  server_id   = local.server_id
  permissions = provider::discord::permissions(["VIEW_CHANNEL", "READ_MESSAGE_HISTORY"])
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_role_everyone.test", "id", env.serverID),
					resource.TestCheckResourceAttr("discord_role_everyone.test", "permissions", "66560"),
				),
			},
			{
				ResourceName:                         "discord_role_everyone.test",
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateId:                        env.serverID,
				ImportStateVerifyIdentifierAttribute: "id",
			},
		},
	})
}

func TestAccRolePositions(t *testing.T) {
	env := newTestEnv(t)
	roles := `
resource "discord_role" "a" {
  server_id = local.server_id
  name      = "tf-acc-a"
}
resource "discord_role" "b" {
  server_id = local.server_id
  name      = "tf-acc-b"
}
resource "discord_role" "c" {
  server_id = local.server_id
  name      = "tf-acc-c"
}
`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(roles + `
resource "discord_role_positions" "test" {
  server_id = local.server_id
  role_ids  = [discord_role.a.id, discord_role.b.id, discord_role.c.id]
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("discord_role_positions.test", "role_ids.0", "discord_role.a", "id"),
					resource.TestCheckResourceAttrPair("discord_role_positions.test", "role_ids.1", "discord_role.b", "id"),
					resource.TestCheckResourceAttrPair("discord_role_positions.test", "role_ids.2", "discord_role.c", "id"),
				),
			},
			{
				Config: env.config(roles + `
resource "discord_role_positions" "test" {
  server_id = local.server_id
  role_ids  = [discord_role.c.id, discord_role.a.id, discord_role.b.id]
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("discord_role_positions.test", "role_ids.0", "discord_role.c", "id"),
					resource.TestCheckResourceAttrPair("discord_role_positions.test", "role_ids.1", "discord_role.a", "id"),
					resource.TestCheckResourceAttrPair("discord_role_positions.test", "role_ids.2", "discord_role.b", "id"),
				),
			},
			{
				Config: env.config(roles + `
resource "discord_role_positions" "test" {
  server_id = local.server_id
  role_ids  = [discord_role.c.id, "123456789012345678"]
}`),
				ExpectError: regexp.MustCompile(`does not exist in server`),
			},
		},
	})
}
