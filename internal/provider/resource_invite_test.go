package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func (e *testEnv) inviteTargets(code *string, want ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := e.client.GetInviteTargetUsers(context.Background(), *code)
		if err != nil {
			return err
		}
		if slices.Sort(want); !slices.Equal(got, want) {
			return fmt.Errorf("invite target users = %v, want %v", got, want)
		}
		return nil
	}
}

func TestAccInviteRolesAndTargetUsers(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	alice := env.fake.AddMember(env.serverID, "alice")
	bob := env.fake.AddMember(env.serverID, "bob")
	carol := env.fake.AddMember(env.serverID, "carol")
	var code, firstCode string
	cfg := func(attrs string) string {
		return env.config(`
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-invite-targets"
}
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-invited"
}
resource "discord_invite" "test" {
  channel_id = discord_text_channel.test.id
` + attrs + `
}`)
	}
	users := func(ids ...string) string {
		return `target_user_ids = ["` + strings.Join(ids, `", "`) + `"]`
	}
	granted := "role_ids = [discord_role.test.id]\n"
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      cfg(`target_user_ids = []`),
				ExpectError: regexp.MustCompile(`set must contain at least 1 elements and at most\s+1000`),
			},
			{
				Config:      cfg(`role_ids = ["role"]`),
				ExpectError: regexp.MustCompile(`must be a Discord snowflake ID`),
			},
			{
				Config: cfg(granted + users(alice, bob)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("discord_invite.test", "role_ids.0", "discord_role.test", "id"),
					resource.TestCheckResourceAttr("discord_invite.test", "role_ids.#", "1"),
					resource.TestCheckTypeSetElemAttr("discord_invite.test", "target_user_ids.*", alice),
					resource.TestCheckTypeSetElemAttr("discord_invite.test", "target_user_ids.*", bob),
					resource.TestCheckNoResourceAttr("discord_invite.test", "target_type"),
					captureAttr("discord_invite.test", "code", &code),
					captureAttr("discord_invite.test", "code", &firstCode),
					env.inviteTargets(&code, alice, bob),
				),
			},
			{
				// Target users are not imported.
				ResourceName:            "discord_invite.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"target_user_ids"},
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return s.RootModule().Resources["discord_text_channel.test"].Primary.ID + "/" + code, nil
				},
			},
			{
				// Target users change in place.
				Config: cfg(granted + users(bob, carol)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_invite.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPtr("discord_invite.test", "code", &firstCode),
					env.inviteTargets(&code, bob, carol),
				),
			},
			{
				// Target users changed outside Terraform are restored.
				PreConfig: func() { env.fake.SetInviteTargetUsers(code, alice) },
				Config:    cfg(granted + users(bob, carol)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_invite.test", plancheck.ResourceActionUpdate)},
				},
				Check: env.inviteTargets(&code, bob, carol),
			},
			{
				// Dropping the target users opens the invite to everyone,
				// which takes a new invite.
				Config: cfg(granted),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_invite.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("discord_invite.test", "target_user_ids"),
					captureAttr("discord_invite.test", "code", &code),
				),
			},
			{
				Config: cfg(""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_invite.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.TestCheckNoResourceAttr("discord_invite.test", "role_ids"),
			},
		},
	})
}

func TestAccInviteTarget(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	cfg := func(attrs string) string {
		return env.config(`
resource "discord_voice_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-invite-target"
}
resource "discord_invite" "test" {
  channel_id = discord_voice_channel.test.id
` + attrs + `
}`)
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      cfg(`target_type = "stream"`),
				ExpectError: regexp.MustCompile(`target_user_id is required when target_type is "stream"`),
			},
			{
				Config:      cfg(`target_user_id = "` + env.userID + `"`),
				ExpectError: regexp.MustCompile(`target_user_id can only be set when target_type is "stream"`),
			},
			{
				Config: cfg(`
  target_type           = "embedded_application"
  target_user_id        = "` + env.userID + `"
  target_application_id = "880218394199220334"`),
				ExpectError: regexp.MustCompile(`target_user_id can only be set when target_type is "stream"`),
			},
			{
				Config:      cfg(`target_type = "video"`),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
			{
				Config: cfg(`
  target_type    = "stream"
  target_user_id = "` + env.userID + `"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_invite.test", "target_type", "stream"),
					resource.TestCheckResourceAttr("discord_invite.test", "target_user_id", env.userID),
					resource.TestCheckNoResourceAttr("discord_invite.test", "target_application_id"),
				),
			},
			{
				ResourceName:      "discord_invite.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					invite := s.RootModule().Resources["discord_invite.test"].Primary
					return invite.Attributes["channel_id"] + "/" + invite.ID, nil
				},
			},
			{
				Config: cfg(`
  target_type           = "embedded_application"
  target_application_id = "880218394199220334"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_invite.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_invite.test", "target_type", "embedded_application"),
					resource.TestCheckResourceAttr("discord_invite.test", "target_application_id", "880218394199220334"),
					resource.TestCheckNoResourceAttr("discord_invite.test", "target_user_id"),
				),
			},
		},
	})
}
