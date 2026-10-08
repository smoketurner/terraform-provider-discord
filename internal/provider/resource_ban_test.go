package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// banReason checks that the user is banned with the given reason, or with
// none when reason is nil.
func (e *testEnv) banReason(userID string, reason *string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		ban, err := e.client.GetBan(context.Background(), e.serverID, userID)
		if err != nil {
			return err
		}
		if (ban.Reason == nil) != (reason == nil) || (reason != nil && *ban.Reason != *reason) {
			return fmt.Errorf("ban reason = %v, want %v", ban.Reason, reason)
		}
		return nil
	}
}

func (e *testEnv) notBanned(userID string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		_, err := e.client.GetBan(context.Background(), e.serverID, userID)
		if !discord.IsNotFound(err) {
			return fmt.Errorf("user %s is still banned (err %w)", userID, err)
		}
		return nil
	}
}

// Banning removes the member, so these tests ban members added to the fake
// rather than DISCORD_TEST_USER_ID.
func TestAccBan(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	userID := env.fake.AddMember(env.serverID, "spammer")
	bans := "/guilds/" + env.serverID + "/bans/" + userID
	cfg := func(attrs string) string {
		return env.config(`
resource "discord_ban" "test" {
  server_id = local.server_id
  user_id   = "` + userID + `"
` + attrs + `
}`)
	}
	var writes int
	env.run(resource.TestCase{
		CheckDestroy: resource.ComposeAggregateTestCheckFunc(
			env.notBanned(userID),
			// Earlier unbans were made outside Terraform without a reason.
			func(*terraform.State) error {
				h := env.fake.RequestHeaders("DELETE " + bans)
				if got := h[len(h)-1].Get("X-Audit-Log-Reason"); got != "appeal%20accepted" {
					return fmt.Errorf("unban X-Audit-Log-Reason = %q", got)
				}
				return nil
			},
		),
		Steps: []resource.TestStep{
			{
				Config:      cfg(`delete_message_seconds = 604801`),
				ExpectError: regexp.MustCompile(`must be between 0 and 604800`),
			},
			{
				Config:      cfg(`delete_message_seconds = -1`),
				ExpectError: regexp.MustCompile(`must be between 0 and 604800`),
			},
			{
				Config:      cfg(`reason = ""`),
				ExpectError: regexp.MustCompile(`character count must be between 1 and 512`),
			},
			{
				Config: cfg(`
  reason                 = "Spam"
  delete_message_seconds = 604800
  audit_log_reason       = "cleanup"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_ban.test", "id", env.serverID+"/"+userID),
					resource.TestCheckResourceAttr("discord_ban.test", "reason", "Spam"),
					resource.TestCheckResourceAttr("discord_ban.test", "delete_message_seconds", "604800"),
					env.banReason(userID, new("Spam")),
					// reason, not audit_log_reason, is sent with the ban.
					func(*terraform.State) error {
						return env.expectReasons(map[string]string{"PUT " + bans: "Spam"})
					},
					func(*terraform.State) error {
						if _, err := env.client.GetMember(context.Background(), env.serverID, userID); !discord.IsNotFound(err) {
							return fmt.Errorf("banned member was not removed (err %w)", err)
						}
						writes = env.writes()
						return nil
					},
				),
			},
			importStep("discord_ban.test", "delete_message_seconds", "audit_log_reason"),
			{
				// Neither argument can change on Discord once the ban exists.
				Config: cfg(`
  reason                 = "Spam"
  delete_message_seconds = 0
  audit_log_reason       = "appeal accepted"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_ban.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_ban.test", "delete_message_seconds", "0"),
					resource.TestCheckResourceAttr("discord_ban.test", "audit_log_reason", "appeal accepted"),
					func(*terraform.State) error {
						if n := env.writes(); n != writes {
							return fmt.Errorf("%d write requests were sent", n-writes)
						}
						return nil
					},
				),
			},
			{
				// The ban is lifted in the Discord client: banned again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.RemoveBan(ctx, env.serverID, userID)
				}),
				Config: cfg(`
  reason           = "Spam"
  audit_log_reason = "appeal accepted"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_ban.test", plancheck.ResourceActionCreate)},
				},
				Check: env.banReason(userID, new("Spam")),
			},
			{
				// The user is unbanned and banned again with another reason
				// in the Discord client: the configured reason is restored.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					if err := c.RemoveBan(ctx, env.serverID, userID); err != nil {
						return err
					}
					return c.CreateBan(discord.WithAuditLogReason(ctx, "Raid"), env.serverID, userID, discord.Payload{})
				}),
				Config: cfg(`
  reason           = "Spam"
  audit_log_reason = "appeal accepted"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_ban.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: env.banReason(userID, new("Spam")),
			},
			{
				Config: cfg(`
  reason           = "Spam and scams"
  audit_log_reason = "appeal accepted"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_ban.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_ban.test", "reason", "Spam and scams"),
					env.banReason(userID, new("Spam and scams")),
				),
			},
		},
	})
}

// Without reason, the ban records the audit log reason that is sent, and
// reason reports it without planning a change.
func TestAccBanReasonFromAuditLogReason(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	resourceUser := env.fake.AddMember(env.serverID, "raider")
	providerUser := env.fake.AddMember(env.serverID, "other-raider")
	silentUser := env.fake.AddMember(env.serverID, "lurker")
	cfg := func(provider string) string {
		return env.config(provider + `
resource "discord_ban" "resource" {
  server_id        = local.server_id
  user_id          = "` + resourceUser + `"
  audit_log_reason = "Raid"
}
resource "discord_ban" "provider" {
  server_id = local.server_id
  user_id   = "` + providerUser + `"
}`)
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg(`
provider "discord" {
  audit_log_reason = "Managed by Terraform"
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_ban.resource", "reason", "Raid"),
					resource.TestCheckResourceAttr("discord_ban.provider", "reason", "Managed by Terraform"),
					env.banReason(resourceUser, new("Raid")),
					env.banReason(providerUser, new("Managed by Terraform")),
				),
			},
			importStep("discord_ban.resource", "audit_log_reason"),
			{
				// The provider reason changing does not rewrite the ban.
				Config: cfg(``),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: env.config(`
resource "discord_ban" "silent" {
  server_id = local.server_id
  user_id   = "` + silentUser + `"
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("discord_ban.silent", "reason"),
					env.banReason(silentUser, nil),
				),
			},
		},
	})
}

func TestAccBanErrors(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				// Discord refuses to ban the server owner.
				Config: env.config(`
resource "discord_ban" "test" {
  server_id = local.server_id
  user_id   = local.user_id
}`),
				ExpectError: regexp.MustCompile(`Unable to ban user(.|\n)*Missing Permissions`),
			},
			{
				Config: env.config(`
resource "discord_ban" "test" {
  server_id = local.server_id
  user_id   = "x"
}`),
				ExpectError: regexp.MustCompile(`must be a Discord snowflake ID`),
			},
		},
	})
}
