package provider

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// A server has at most one template, so live runs skip these tests rather
// than replace a template the test server may already have.
func TestAccServerTemplate(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	var code string
	cfg := func(attrs string) string {
		return env.config(`
resource "discord_server_template" "test" {
  server_id = local.server_id
` + attrs + `
}`)
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      cfg(`name = "` + strings.Repeat("n", 101) + `"`),
				ExpectError: regexp.MustCompile(`character count must be between 1 and 100`),
			},
			{
				Config:      cfg(`name = "Community"` + "\n" + `description = "` + strings.Repeat("d", 121) + `"`),
				ExpectError: regexp.MustCompile(`character count must be between 1 and 120`),
			},
			{
				Config: cfg(`
  name        = "Community"
  description = "Roles and channels for a community server"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("discord_server_template.test", "id", "discord_server_template.test", "code"),
					resource.TestCheckResourceAttr("discord_server_template.test", "name", "Community"),
					resource.TestCheckResourceAttr("discord_server_template.test", "description", "Roles and channels for a community server"),
					resource.TestCheckResourceAttr("discord_server_template.test", "is_dirty", "false"),
					resource.TestCheckResourceAttr("discord_server_template.test", "usage_count", "0"),
					resource.TestCheckResourceAttrSet("discord_server_template.test", "creator_id"),
					resource.TestCheckResourceAttrSet("discord_server_template.test", "created_at"),
					resource.TestCheckResourceAttrSet("discord_server_template.test", "updated_at"),
					captureAttr("discord_server_template.test", "code", &code),
				),
			},
			{
				ResourceName:      "discord_server_template.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return env.serverID + "/" + code, nil },
			},
			{
				// Changes since the last sync show without a plan.
				PreConfig: func() { env.fake.MarkTemplateDirty(code) },
				Config: cfg(`
  name        = "Community"
  description = "Roles and channels for a community server"`),
				PlanOnly: true,
			},
			{
				Config: cfg(`name = "Gaming"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_server_template.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPtr("discord_server_template.test", "id", &code),
					resource.TestCheckResourceAttr("discord_server_template.test", "name", "Gaming"),
					resource.TestCheckNoResourceAttr("discord_server_template.test", "description"),
					resource.TestCheckResourceAttr("discord_server_template.test", "is_dirty", "true"),
				),
			},
			{
				// Renamed in the Discord client: renamed back.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyGuildTemplate(ctx, env.serverID, code, discord.Payload{"name": "Renamed", "description": "Edited"})
					return err
				}),
				Config: cfg(`name = "Gaming"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_server_template.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_server_template.test", "name", "Gaming"),
					resource.TestCheckNoResourceAttr("discord_server_template.test", "description"),
				),
			},
			{
				// Deleted in the Discord client: created again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteGuildTemplate(ctx, env.serverID, code)
				}),
				Config: cfg(`name = "Gaming"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_server_template.test", plancheck.ResourceActionCreate)},
				},
				Check: resource.TestCheckResourceAttrWith("discord_server_template.test", "code", func(v string) error {
					if v == code {
						return errors.New("the deleted template's code is still in state")
					}
					return nil
				}),
			},
			{
				// Discord allows one template per server.
				Config: env.config(`
resource "discord_server_template" "test" {
  server_id = local.server_id
  name      = "Gaming"
}
resource "discord_server_template" "second" {
  server_id = local.server_id
  name      = "Second"
}`),
				ExpectError: regexp.MustCompile(`Guild already has a template`),
			},
		},
	})
}
