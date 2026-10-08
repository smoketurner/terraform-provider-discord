package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

const incidentActionsAddress = "discord_server_incident_actions.test"

// advanceClock moves both the provider's and the fake's clocks forward.
func (e *testEnv) advanceClock(d time.Duration) func() {
	return func() {
		clockSkew.Add(int64(d))
		e.fake.AdvanceClock(d)
	}
}

// expectIncidentActions checks the actions Discord reports. An empty want
// means the action is not active.
func (e *testEnv) expectIncidentActions(invites, dms string) resource.TestCheckFunc {
	return e.checkGuild(func(g *discord.Guild) error {
		d := g.IncidentsData
		if d == nil {
			d = &discord.IncidentsData{}
		}
		for name, got := range map[string]*string{"invites": d.InvitesDisabledUntil, "dms": d.DMsDisabledUntil} {
			want := map[string]string{"invites": invites, "dms": dms}[name]
			switch {
			case want == "" && got != nil:
				return fmt.Errorf("%s disabled until %s, want not disabled", name, *got)
			case want == "":
			case got == nil:
				return fmt.Errorf("%s not disabled, want until %s", name, want)
			default:
				w, _ := time.Parse(time.RFC3339, want)
				g, err := time.Parse(time.RFC3339Nano, *got)
				if err != nil || !g.Equal(w) {
					return fmt.Errorf("%s disabled until %s, want %s", name, *got, want)
				}
			}
		}
		return nil
	})
}

func TestAccServerIncidentActions(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	t.Cleanup(func() { clockSkew.Store(0) })
	start := time.Now().UTC().Truncate(time.Second)
	in := func(d time.Duration) string { return start.Add(d).Format(time.RFC3339) }
	actions := func(attrs string) string {
		return env.config(`
resource "discord_server_incident_actions" "test" {
  server_id = local.server_id
` + attrs + `
}`)
	}
	invitesOnly := actions(`  invites_disabled_until = "` + in(time.Hour) + `"`)
	both := actions(`  invites_disabled_until = "` + in(time.Hour) + `"
  dms_disabled_until     = "` + in(2*time.Hour) + `"`)
	expectUpdate := resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(incidentActionsAddress, plancheck.ResourceActionUpdate)},
	}
	env.run(resource.TestCase{
		CheckDestroy: env.expectIncidentActions("", ""),
		Steps: []resource.TestStep{
			{
				Config:      actions(`  invites_disabled_until = "tomorrow"`),
				ExpectError: regexp.MustCompile(`must be an RFC 3339 timestamp`),
			},
			{
				Config:      actions(`  invites_disabled_until = "` + in(25*time.Hour) + `"`),
				ExpectError: regexp.MustCompile(`invites_disabled_until must be at most 24 hours from now`),
			},
			{
				Config:      actions(`  dms_disabled_until = "` + in(-time.Minute) + `"`),
				ExpectError: regexp.MustCompile(`dms_disabled_until must be in the future`),
			},
			{
				Config: invitesOnly,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(incidentActionsAddress, "id", env.serverID),
					resource.TestCheckResourceAttr(incidentActionsAddress, "invites_disabled_until", in(time.Hour)),
					resource.TestCheckNoResourceAttr(incidentActionsAddress, "dms_disabled_until"),
					env.expectIncidentActions(in(time.Hour), ""),
				),
			},
			{
				Config:           both,
				ConfigPlanChecks: expectUpdate,
				Check:            env.expectIncidentActions(in(time.Hour), in(2*time.Hour)),
			},
			{
				// Discord reports timestamps in its own format.
				ResourceName:  incidentActionsAddress,
				ImportState:   true,
				ImportStateId: env.serverID,
				ImportStateCheck: func(s []*terraform.InstanceState) error {
					if len(s) != 1 || s[0].Attributes["invites_disabled_until"] == "" || s[0].Attributes["dms_disabled_until"] == "" {
						return fmt.Errorf("imported state %v lacks the actions", s)
					}
					return nil
				},
			},
			{
				// The actions are lifted in the Discord client: set again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.ModifyGuildIncidentActions(ctx, env.serverID, discord.Payload{"invites_disabled_until": nil, "dms_disabled_until": nil})
					return err
				}),
				Config:           both,
				ConfigPlanChecks: expectUpdate,
				Check:            env.expectIncidentActions(in(time.Hour), in(2*time.Hour)),
			},
			{
				// The invite pause ends on its own: not drift.
				PreConfig: env.advanceClock(90 * time.Minute),
				Config:    both,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(incidentActionsAddress, "invites_disabled_until", in(time.Hour)),
					env.expectIncidentActions("", in(2*time.Hour)),
				),
			},
			{
				// Changing the other action sends the expired one as lifted.
				Config: actions(`  invites_disabled_until = "` + in(time.Hour) + `"
  dms_disabled_until     = "` + in(3*time.Hour) + `"`),
				ConfigPlanChecks: expectUpdate,
				Check:            env.expectIncidentActions("", in(3*time.Hour)),
			},
			{
				// A new timestamp starts the pause again.
				Config: actions(`  invites_disabled_until = "` + in(4*time.Hour) + `"
  dms_disabled_until     = "` + in(3*time.Hour) + `"`),
				ConfigPlanChecks: expectUpdate,
				Check:            env.expectIncidentActions(in(4*time.Hour), in(3*time.Hour)),
			},
			{
				Config:           actions(""),
				ConfigPlanChecks: expectUpdate,
				Check:            env.expectIncidentActions("", ""),
			},
		},
	})
}

func TestAccServerIncidentActionsServerGone(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	config := env.config(`
resource "discord_server_incident_actions" "test" {
  server_id          = local.server_id
  dms_disabled_until = "` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"
}`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{Config: config},
			{
				// The bot is removed from the server: the resource leaves state.
				PreConfig:          func() { env.fake.RemoveGuild(env.serverID) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}
