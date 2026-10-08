package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// checkOverwrites verifies a channel's permission overwrites in Discord,
// given as "allow/deny" by overwrite ID. IDs prefixed with "role:" are read
// from that resource's id attribute.
func (e *testEnv) checkOverwrites(channel string, want map[string]string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[channel]
		if !ok {
			return fmt.Errorf("resource %s not found in state", channel)
		}
		ch, err := e.client.GetChannel(context.Background(), rs.Primary.ID)
		if err != nil {
			return err
		}
		got := map[string]string{}
		for _, o := range ch.PermissionOverwrites {
			got[o.ID] = o.Allow + "/" + o.Deny
		}
		resolved := map[string]string{}
		for id, v := range want {
			if ref, ok := s.RootModule().Resources[id]; ok {
				id = ref.Primary.ID
			}
			resolved[id] = v
		}
		if fmt.Sprint(got) != fmt.Sprint(resolved) {
			return fmt.Errorf("%s overwrites = %v, want %v", channel, got, resolved)
		}
		return nil
	}
}

func TestAccChannelInitialPermissionOverwrites(t *testing.T) {
	env := newTestEnv(t)
	const roleAndChannels = `
resource "discord_role" "members" {
  server_id = local.server_id
  name      = "tf-acc-overwrites"
}
`
	private := func(everyone string) string {
		return env.config(roleAndChannels + fmt.Sprintf(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-private"

  initial_permission_overwrites = [
    {
      id   = local.server_id
      type = "role"
      deny = %q
    },
    {
      id    = discord_role.members.id
      type  = "role"
      allow = "1024"
    },
  ]
}
resource "discord_category_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-private-category"

  initial_permission_overwrites = [{
    id    = local.server_id
    type  = "role"
    allow = "0"
    deny  = "1024"
  }]
}
# Takes over the initial overwrite for the role.
resource "discord_channel_permission" "members" {
  channel_id   = discord_text_channel.test.id
  overwrite_id = discord_role.members.id
  type         = "role"
  allow        = "3072"
}
`, everyone))
	}
	var channelID string
	var writes int
	countWrites := func() {
		if env.fake != nil {
			writes = env.writes()
		}
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: private("1024"),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("discord_text_channel.test", "id", &channelID),
					resource.TestCheckResourceAttr("discord_text_channel.test", "initial_permission_overwrites.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("discord_text_channel.test", "initial_permission_overwrites.*",
						map[string]string{"type": "role", "deny": "1024"}),
					resource.TestCheckResourceAttr("discord_channel_permission.members", "allow", "3072"),
					resource.TestCheckResourceAttr("discord_channel_permission.members", "deny", "0"),
					env.checkOverwrites("discord_text_channel.test", map[string]string{
						env.serverID: "0/1024", "discord_role.members": "3072/0",
					}),
					env.checkOverwrites("discord_category_channel.test", map[string]string{env.serverID: "0/1024"}),
				),
			},
			importStep("discord_text_channel.test", "initial_permission_overwrites"),
			importStep("discord_category_channel.test", "initial_permission_overwrites"),
			{
				// Changing the initial overwrites only updates state.
				PreConfig: countWrites,
				Config:    private("3072"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_text_channel.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckTypeSetElemNestedAttrs("discord_text_channel.test", "initial_permission_overwrites.*",
						map[string]string{"type": "role", "deny": "3072"}),
					env.checkOverwrites("discord_text_channel.test", map[string]string{
						env.serverID: "0/1024", "discord_role.members": "3072/0",
					}),
					func(*terraform.State) error {
						if env.fake != nil && env.writes() != writes {
							return fmt.Errorf("changing initial_permission_overwrites sent requests: %v", env.fake.Requests())
						}
						return nil
					},
				),
			},
			{
				// The initial overwrites are not refreshed, so removing one
				// outside Terraform is not drift.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteChannelPermission(ctx, channelID, env.serverID)
				}),
				Config: private("3072"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: env.checkOverwrites("discord_text_channel.test", map[string]string{"discord_role.members": "3072/0"}),
			},
			{
				// Removing the attribute neither replaces the channel nor
				// removes its overwrites.
				Config: env.config(roleAndChannels + `
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-private-renamed"
}
resource "discord_category_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-private-category"
}
resource "discord_channel_permission" "members" {
  channel_id   = discord_text_channel.test.id
  overwrite_id = discord_role.members.id
  type         = "role"
  allow        = "3072"
}
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_text_channel.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("discord_category_channel.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("discord_text_channel.test", "initial_permission_overwrites"),
					env.checkOverwrites("discord_text_channel.test", map[string]string{"discord_role.members": "3072/0"}),
					env.checkOverwrites("discord_category_channel.test", map[string]string{env.serverID: "0/1024"}),
				),
			},
		},
	})
}

func TestAccChannelInitialPermissionOverwritesSentOnCreate(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
resource "discord_media_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-private-media"
  nsfw      = true

  initial_permission_overwrites = [{
    id    = local.user_id
    type  = "member"
    allow = "2048"
  }]
}
`),
			Check: resource.ComposeAggregateTestCheckFunc(
				env.checkOverwrites("discord_media_channel.test", map[string]string{env.userID: "2048/0"}),
				func(*terraform.State) error {
					// The overwrites go in the create request; no separate
					// permission request is made.
					if i := slices.IndexFunc(env.fake.Requests(), func(k string) bool {
						return regexp.MustCompile(`^PUT /channels/\d+/permissions/`).MatchString(k)
					}); i >= 0 {
						return fmt.Errorf("unexpected request %s", env.fake.Requests()[i])
					}
					return nil
				},
			),
		}},
	})
}

func TestAccChannelInitialPermissionOverwritesValidation(t *testing.T) {
	env := newTestEnv(t)
	overwrite := func(fields, want string) resource.TestStep {
		return resource.TestStep{
			Config: env.config(fmt.Sprintf(`
resource "discord_voice_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-invalid"

  initial_permission_overwrites = [{
    %s
  }]
}
`, fields)),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(want),
		}
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			overwrite(`id = "not-a-snowflake"
    type = "role"`, `must be a Discord snowflake ID`),
			overwrite(`id = local.server_id
    type = "channel"`, `value must be one of`),
			overwrite(`id = local.server_id
    type = "role"
    allow = "-1"`, `must be a decimal permission bitfield`),
			overwrite(`id = local.server_id
    type = "member"
    deny = "0x400"`, `must be a decimal permission bitfield`),
		},
	})
}
