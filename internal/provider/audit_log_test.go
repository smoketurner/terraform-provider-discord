package provider

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// auditReasons returns the decoded X-Audit-Log-Reason of every request the
// fake received, keyed by "METHOD /path". Requests without the header map to
// "".
func (e *testEnv) auditReasons() map[string][]string {
	out := map[string][]string{}
	for _, key := range slices.Compact(slices.Sorted(slices.Values(e.fake.Requests()))) {
		for _, h := range e.fake.RequestHeaders(key) {
			reason, err := url.PathUnescape(h.Get("X-Audit-Log-Reason"))
			if err != nil {
				reason = "invalid encoding: " + h.Get("X-Audit-Log-Reason")
			}
			out[key] = append(out[key], reason)
		}
	}
	return out
}

// unaudited reports whether Discord ignores X-Audit-Log-Reason on a request,
// so the provider must not send it.
func unaudited(key string) bool {
	method, path, _ := strings.Cut(key, " ")
	return method == "GET" ||
		(method == "POST" && strings.HasSuffix(path, "/messages")) ||
		(method == "PATCH" && strings.Contains(path, "/messages/")) ||
		(method == "PATCH" && strings.HasPrefix(path, "/guilds/") && strings.HasSuffix(path, "/channels"))
}

// checkAuditReasons verifies every request the fake received carried the
// reason want returns for it, and that every key in mustSee was requested.
func (e *testEnv) checkAuditReasons(want func(key string) string, mustSee ...string) error {
	reasons := e.auditReasons()
	for _, key := range mustSee {
		if _, ok := reasons[key]; !ok {
			return fmt.Errorf("no %s request was sent", key)
		}
	}
	for key, got := range reasons {
		expected := want(key)
		if unaudited(key) {
			expected = ""
		}
		for _, reason := range got {
			if reason != expected {
				return fmt.Errorf("%s X-Audit-Log-Reason = %q, want %q", key, reason, expected)
			}
		}
	}
	return nil
}

func TestAccAuditLogReasonProvider(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	g := "/guilds/" + env.serverID
	const reason = "Managed by Terraform ✨ 100% & more"
	everywhere := func(string) string { return reason }
	var channelID string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(fmt.Sprintf(`
provider "discord" {
  audit_log_reason = %q
}
resource "discord_role" "low" {
  server_id = local.server_id
  name      = "tf-acc-audit-low"
}
resource "discord_role" "high" {
  server_id  = local.server_id
  name       = "tf-acc-audit-high"
  depends_on = [discord_role.low]
}
# The fake puts new roles at the bottom, so this order needs a reorder.
resource "discord_role_positions" "test" {
  server_id = local.server_id
  role_ids  = [discord_role.high.id, discord_role.low.id]
}
resource "discord_text_channel" "a" {
  server_id = local.server_id
  name      = "tf-acc-audit-a"
}
resource "discord_text_channel" "b" {
  server_id  = local.server_id
  name       = "tf-acc-audit-b"
  depends_on = [discord_text_channel.a]
}
# Tied positions sort by ID, so b is listed below a until it is moved.
resource "discord_channel_positions" "test" {
  server_id   = local.server_id
  channel_ids = [discord_text_channel.b.id, discord_text_channel.a.id]
}
resource "discord_message" "test" {
  channel_id = discord_text_channel.a.id
  content    = "audited elsewhere"
  pinned     = true
}`, reason)),
			Check: resource.ComposeAggregateTestCheckFunc(
				captureAttr("discord_text_channel.a", "id", &channelID),
				func(*terraform.State) error {
					c := "/channels/" + channelID
					return env.checkAuditReasons(everywhere,
						"POST "+g+"/roles", "PATCH "+g+"/roles", "POST "+g+"/channels", "PATCH "+g+"/channels",
						"POST "+c+"/messages", "GET "+g+"/roles")
				},
			),
		}},
		CheckDestroy: func(*terraform.State) error {
			return env.checkAuditReasons(everywhere, "DELETE /channels/"+channelID)
		},
	})
}

// expectReasons verifies that each "METHOD /path" was requested and always
// carried the given reason.
func (e *testEnv) expectReasons(want map[string]string) error {
	reasons := e.auditReasons()
	for key, reason := range want {
		got := reasons[key]
		if len(got) == 0 {
			return fmt.Errorf("no %s request was sent", key)
		}
		for _, r := range got {
			if r != reason {
				return fmt.Errorf("%s X-Audit-Log-Reason = %q, want %q", key, got, reason)
			}
		}
	}
	return nil
}

// writes counts the non-GET requests the fake received.
func (e *testEnv) writes() int {
	n := 0
	for _, key := range e.fake.Requests() {
		if !strings.HasPrefix(key, "GET ") {
			n++
		}
	}
	return n
}

func TestAccAuditLogReasonResourceOverride(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	g := "/guilds/" + env.serverID
	const providerReason = "Managed by Terraform"
	config := func(roleName, roleReason, inviteReason string) string {
		return env.config(fmt.Sprintf(`
provider "discord" {
  audit_log_reason = %q
}
resource "discord_role" "test" {
  server_id        = local.server_id
  name             = %q
  audit_log_reason = %q
}
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-audit"
}
resource "discord_invite" "test" {
  channel_id       = discord_text_channel.test.id
  audit_log_reason = %q
}`, providerReason, roleName, roleReason, inviteReason))
	}
	var roleID, channelID, inviteCode string
	var writes int
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: config("tf-acc-audit", "Created for ops", "Invite reason"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_role.test", "audit_log_reason", "Created for ops"),
					captureAttr("discord_role.test", "id", &roleID),
					captureAttr("discord_text_channel.test", "id", &channelID),
					captureAttr("discord_invite.test", "id", &inviteCode),
					func(*terraform.State) error {
						writes = env.writes()
						return env.expectReasons(map[string]string{
							"POST " + g + "/roles":                     "Created for ops",
							"POST " + g + "/channels":                  providerReason,
							"POST /channels/" + channelID + "/invites": "Invite reason",
						})
					},
				),
			},
			{
				// Changing only the reason is an in-place update that sends
				// nothing, even for a resource whose other arguments all
				// force replacement.
				Config: config("tf-acc-audit", "Rotated by ops", "Rotated invite"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("discord_role.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("discord_invite.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("discord_text_channel.test", plancheck.ResourceActionNoop),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("discord_role.test", "audit_log_reason", "Rotated by ops"),
					resource.TestCheckResourceAttr("discord_invite.test", "audit_log_reason", "Rotated invite"),
					func(*terraform.State) error {
						if n := env.writes(); n != writes {
							return fmt.Errorf("a reason-only change sent %d requests: %v", n-writes, env.fake.Requests())
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "discord_role.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"audit_log_reason"},
				ImportStateIdFunc:       func(*terraform.State) (string, error) { return env.serverID + "/" + roleID, nil },
			},
			{
				Config: config("tf-acc-audit-renamed", "Rotated by ops", "Rotated invite"),
				Check: func(*terraform.State) error {
					return env.expectReasons(map[string]string{"PATCH " + g + "/roles/" + roleID: "Rotated by ops"})
				},
			},
		},
		CheckDestroy: func(*terraform.State) error {
			return env.expectReasons(map[string]string{
				"DELETE " + g + "/roles/" + roleID: "Rotated by ops",
				"DELETE /invites/" + inviteCode:    "Rotated invite",
				"DELETE /channels/" + channelID:    providerReason,
			})
		},
	})
}

// The follow-up modify that sets nsfw on a new media channel carries the
// resource's reason too.
func TestAccAuditLogReasonMediaChannelCreate(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	var channelID string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
resource "discord_media_channel" "test" {
  server_id        = local.server_id
  name             = "tf-acc-audit-media"
  nsfw             = true
  audit_log_reason = "Media for ops"
}`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("discord_media_channel.test", "nsfw", "true"),
				captureAttr("discord_media_channel.test", "id", &channelID),
				func(*terraform.State) error {
					return env.expectReasons(map[string]string{
						"POST /guilds/" + env.serverID + "/channels": "Media for ops",
						"PATCH /channels/" + channelID:               "Media for ops",
					})
				},
			),
		}},
	})
}

func TestAccAuditLogReasonEnvironment(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	t.Setenv("DISCORD_AUDIT_LOG_REASON", "From the environment")
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-audit-env"
}`),
			Check: func(*terraform.State) error {
				return env.expectReasons(map[string]string{"POST /guilds/" + env.serverID + "/roles": "From the environment"})
			},
		}},
	})
}

func TestAccAuditLogReasonNotSentByDefault(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
resource "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-audit-none"
}`),
			Check: func(*terraform.State) error {
				return env.checkAuditReasons(func(string) string { return "" }, "POST /guilds/"+env.serverID+"/roles")
			},
		}},
	})
}

func TestAuditLogReasonValidation(t *testing.T) {
	long := strings.Repeat("é", 513)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
provider "discord" {
  token            = "x"
  audit_log_reason = %q
}
data "discord_server" "s" { id = "1" }`, long),
				ExpectError: regexp.MustCompile(`character count must be between 1 and 512`),
			},
			{
				Config: `
provider "discord" {
  token = "x"
}
resource "discord_role" "test" {
  server_id        = "1"
  name             = "r"
  audit_log_reason = ""
}`,
				ExpectError: regexp.MustCompile(`character count must be between 1 and 512`),
			},
		},
	})
}

func TestAuditLogReasonEnvironmentTooLong(t *testing.T) {
	t.Setenv("DISCORD_TOKEN", "x")
	t.Setenv("DISCORD_AUDIT_LOG_REASON", strings.Repeat("a", 513))
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      `data "discord_server" "s" { id = "1" }`,
			ExpectError: regexp.MustCompile(`DISCORD_AUDIT_LOG_REASON is 513 characters`),
		}},
	})
}
