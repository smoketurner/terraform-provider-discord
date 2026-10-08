package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// checkMember runs check on the member as Discord has it.
func (e *testEnv) checkMember(userID string, check func(m *discord.Member) error) resource.TestCheckFunc {
	return func(*terraform.State) error {
		m, err := e.client.GetMember(context.Background(), e.serverID, userID)
		if err != nil {
			return err
		}
		return check(m)
	}
}

// memberHasRoles checks that the member has exactly the roles in the given
// resource attributes plus the given role IDs.
func (e *testEnv) memberHasRoles(userID string, roleAttrs []string, ids ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		want := slices.Clone(ids)
		for _, attr := range roleAttrs {
			rs, ok := s.RootModule().Resources[attr]
			if !ok {
				return fmt.Errorf("resource %s not found in state", attr)
			}
			want = append(want, rs.Primary.ID)
		}
		return e.checkMember(userID, func(m *discord.Member) error {
			got := slices.Sorted(slices.Values(m.Roles))
			slices.Sort(want)
			if !slices.Equal(got, want) {
				return fmt.Errorf("member roles = %v, want %v", got, want)
			}
			return nil
		})(s)
	}
}

func TestAccMemberRoles(t *testing.T) {
	env := newTestEnv(t)
	env.requireUser()
	roles := `
resource "discord_role" "a" {
  server_id = local.server_id
  name      = "tf-acc-roles-a"
}
resource "discord_role" "b" {
  server_id = local.server_id
  name      = "tf-acc-roles-b"
}
resource "discord_role" "extra" {
  server_id = local.server_id
  name      = "tf-acc-roles-extra"
}
`
	withRoles := func(ids string) string {
		return env.config(roles + `
resource "discord_member_roles" "test" {
  server_id        = local.server_id
  user_id          = local.user_id
  role_ids         = [` + ids + `]
  audit_log_reason = "roster"
}`)
	}
	var extraID string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: withRoles(`local.server_id`),
				// @everyone is the server ID.
				ExpectError: regexp.MustCompile(`must not contain the @everyone role`),
			},
			{
				Config:      withRoles(`"x"`),
				ExpectError: regexp.MustCompile(`must be a Discord snowflake ID`),
			},
			{
				Config: withRoles(`discord_role.a.id`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_member_roles.test", "id", env.serverID+"/"+env.userID),
					resource.TestCheckResourceAttr("discord_member_roles.test", "role_ids.#", "1"),
					resource.TestCheckTypeSetElemAttrPair("discord_member_roles.test", "role_ids.*", "discord_role.a", "id"),
					env.memberHasRoles(env.userID, []string{"discord_role.a"}),
					captureAttr("discord_role.extra", "id", &extraID),
				),
			},
			importStep("discord_member_roles.test", "audit_log_reason"),
			{
				Config: withRoles(`discord_role.a.id, discord_role.b.id`),
				Check:  env.memberHasRoles(env.userID, []string{"discord_role.a", "discord_role.b"}),
			},
			{
				// A role granted outside Terraform is revoked.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.AddMemberRole(ctx, env.serverID, env.userID, extraID)
				}),
				Config: withRoles(`discord_role.a.id, discord_role.b.id`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_member_roles.test", plancheck.ResourceActionUpdate)},
				},
				Check: env.memberHasRoles(env.userID, []string{"discord_role.a", "discord_role.b"}),
			},
			{
				// A role revoked outside Terraform is granted again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					m, err := c.GetMember(ctx, env.serverID, env.userID)
					if err != nil {
						return err
					}
					return c.RemoveMemberRole(ctx, env.serverID, env.userID, m.Roles[0])
				}),
				Config: withRoles(`discord_role.a.id, discord_role.b.id`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_member_roles.test", plancheck.ResourceActionUpdate)},
				},
				Check: env.memberHasRoles(env.userID, []string{"discord_role.a", "discord_role.b"}),
			},
			{
				Config: withRoles(``),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_member_roles.test", "role_ids.#", "0"),
					env.memberHasRoles(env.userID, nil),
				),
			},
			{
				Config: withRoles(`discord_role.b.id`),
				Check:  env.memberHasRoles(env.userID, []string{"discord_role.b"}),
			},
			{
				// Destroying revokes the listed roles.
				Config: env.config(roles),
				Check:  env.memberHasRoles(env.userID, nil),
			},
		},
	})
}

func TestAccMemberRolesManaged(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	managed := env.fake.AddManagedRole(env.serverID, "Server Booster", env.userID)
	cfg := func(ids string) string {
		return env.config(`
resource "discord_role" "a" {
  server_id = local.server_id
  name      = "tf-acc-roles-a"
}
resource "discord_member_roles" "test" {
  server_id = local.server_id
  user_id   = local.user_id
  role_ids  = [` + ids + `]
}`)
	}
	env.run(resource.TestCase{
		CheckDestroy: env.memberHasRoles(env.userID, nil, managed),
		Steps: []resource.TestStep{
			{
				Config:      cfg(`"` + managed + `"`),
				ExpectError: regexp.MustCompile(`Role ` + managed + ` is managed by Discord`),
			},
			{
				// The managed role is kept and is not part of role_ids.
				Config: cfg(`discord_role.a.id`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_member_roles.test", "role_ids.#", "1"),
					env.memberHasRoles(env.userID, []string{"discord_role.a"}, managed),
				),
			},
			importStep("discord_member_roles.test"),
			{
				Config: cfg(``),
				Check:  env.memberHasRoles(env.userID, nil, managed),
			},
		},
	})
}

func TestAccMemberRolesMemberLeft(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	userID := env.fake.AddMember(env.serverID, "leaver")
	cfg := env.config(`
resource "discord_member_roles" "test" {
  server_id = local.server_id
  user_id   = "` + userID + `"
  role_ids  = []
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				// A member who left is planned for creation, and destroying
				// the resource succeeds.
				PreConfig:          func() { env.fake.RemoveMember(env.serverID, userID) },
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config:      cfg,
				ExpectError: regexp.MustCompile(`Unknown\s+Member`),
			},
		},
	})
}

// timestamp formats a time relative to now as Terraform configuration
// would.
func timestamp(d time.Duration) string {
	return time.Now().Add(d).UTC().Truncate(time.Second).Format(time.RFC3339)
}

func TestAccMember(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	userID := env.fake.AddMember(env.serverID, "bob")
	until := timestamp(24 * time.Hour)
	later := timestamp(48 * time.Hour)
	past := "2020-01-01T00:00:00Z"
	cfg := func(args string) string {
		return env.config(`
resource "discord_member" "test" {
  server_id = local.server_id
  user_id   = "` + userID + `"
` + args + `
}`)
	}
	member := func(nick, timeout *string) resource.TestCheckFunc {
		return env.checkMember(userID, func(m *discord.Member) error {
			if deref(m.Nick) != deref(nick) || deref(m.CommunicationDisabledUntil) != deref(timeout) {
				return fmt.Errorf("member nick = %q, timeout = %q; want %q, %q",
					deref(m.Nick), deref(m.CommunicationDisabledUntil), deref(nick), deref(timeout))
			}
			return nil
		})
	}
	bob, other := "Bob", "Other"
	env.run(resource.TestCase{
		CheckDestroy: member(nil, nil),
		Steps: []resource.TestStep{
			{Config: cfg(`nick = "` + strings.Repeat("n", 33) + `"`), ExpectError: regexp.MustCompile(`character count must be between 1 and 32`)},
			{Config: cfg(`nick = " Bob"`), ExpectError: regexp.MustCompile(`must not start or end with whitespace`)},
			{Config: cfg(`communication_disabled_until = "tomorrow"`), ExpectError: regexp.MustCompile(`must be an RFC 3339 timestamp`)},
			{Config: cfg(`communication_disabled_until = "` + timestamp(29*24*time.Hour) + `"`), ExpectError: regexp.MustCompile(`at most 28 days from now`)},
			{
				Config: cfg(`
  nick                         = "Bob"
  communication_disabled_until = "` + until + `"
  audit_log_reason             = "spam"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_member.test", "id", env.serverID+"/"+userID),
					member(&bob, &until),
					func(*terraform.State) error {
						h := env.fake.RequestHeaders("PATCH /guilds/" + env.serverID + "/members/" + userID)
						if len(h) == 0 || h[len(h)-1].Get("X-Audit-Log-Reason") != "spam" {
							return fmt.Errorf("audit log reason not sent: %v", h)
						}
						return nil
					},
				),
			},
			importStep("discord_member.test", "audit_log_reason"),
			{
				// A nickname and timeout changed outside Terraform are set
				// again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyMember(ctx, env.serverID, userID, discord.Payload{"nick": other, "communication_disabled_until": later})
					return err
				}),
				Config: cfg(`
  nick                         = "Bob"
  communication_disabled_until = "` + until + `"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_member.test", plancheck.ResourceActionUpdate)},
				},
				Check: member(&bob, &until),
			},
			{
				// A timeout ended early outside Terraform is set again.
				PreConfig: func() { env.fake.SetMemberTimeout(env.serverID, userID, nil) },
				Config: cfg(`
  nick                         = "Bob"
  communication_disabled_until = "` + until + `"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_member.test", plancheck.ResourceActionUpdate)},
				},
				Check: member(&bob, &until),
			},
			{
				// Discord may format the time differently.
				PreConfig: func() {
					t, _ := time.Parse(time.RFC3339, until)
					v := t.Format("2006-01-02T15:04:05.000000-07:00")
					env.fake.SetMemberTimeout(env.serverID, userID, &v)
				},
				Config: cfg(`
  nick                         = "Bob"
  communication_disabled_until = "` + until + `"`),
				PlanOnly: true,
			},
			{
				// A time in the past ends the timeout.
				Config: cfg(`
  nick                         = "Bob"
  communication_disabled_until = "` + past + `"`),
				Check: member(&bob, nil),
			},
			{
				// An expired timeout is not drift.
				PreConfig: func() {
					v := "2021-06-01T00:00:00+00:00"
					env.fake.SetMemberTimeout(env.serverID, userID, &v)
				},
				Config: cfg(`
  nick                         = "Bob"
  communication_disabled_until = "` + past + `"`),
				PlanOnly: true,
			},
			{
				// A timeout set outside Terraform is ended while the argument
				// is in the past.
				PreConfig: func() { env.fake.SetMemberTimeout(env.serverID, userID, &later) },
				Config: cfg(`
  nick                         = "Bob"
  communication_disabled_until = "` + past + `"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("discord_member.test", plancheck.ResourceActionUpdate)},
				},
				Check: member(&bob, nil),
			},
			{
				// Removing the arguments clears the nickname and ends the
				// timeout.
				Config: cfg(``),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("discord_member.test", "nick"),
					resource.TestCheckNoResourceAttr("discord_member.test", "communication_disabled_until"),
					member(nil, nil),
				),
			},
			{
				// Unset arguments are not managed.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyMember(ctx, env.serverID, userID, discord.Payload{"nick": other, "communication_disabled_until": later})
					return err
				}),
				Config:   cfg(``),
				PlanOnly: true,
			},
			{
				// Import reads the nickname and the active timeout.
				ResourceName:  "discord_member.test",
				ImportState:   true,
				ImportStateId: env.serverID + "/" + userID,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					a := states[0].Attributes
					if a["nick"] != other || a["communication_disabled_until"] != later {
						return fmt.Errorf("imported nick = %q, timeout = %q", a["nick"], a["communication_disabled_until"])
					}
					return nil
				},
			},
			{
				Config: cfg(`
  nick                         = "Bob"
  communication_disabled_until = "` + until + `"`),
				Check: member(&bob, &until),
			},
		},
	})
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestAccMemberOwnerTimeout(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			// The seeded member owns the server, which Discord refuses to
			// time out.
			Config: env.config(`
resource "discord_member" "test" {
  server_id                    = local.server_id
  user_id                      = local.user_id
  communication_disabled_until = "` + timestamp(time.Hour) + `"
}`),
			ExpectError: regexp.MustCompile(`Missing\s+Permissions`),
		}},
	})
}

func TestAccMemberLeft(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	userID := env.fake.AddMember(env.serverID, "leaver")
	cfg := env.config(`
resource "discord_member" "test" {
  server_id = local.server_id
  user_id   = "` + userID + `"
  nick      = "Leaver"
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig:          func() { env.fake.RemoveMember(env.serverID, userID) },
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config:      cfg,
				ExpectError: regexp.MustCompile(`Unknown\s+Member`),
			},
		},
	})
}
