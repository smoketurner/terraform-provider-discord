package provider

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
)

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func TestAccDataSources(t *testing.T) {
	env := newTestEnv(t)
	resources := `
resource "discord_role" "test" {
  server_id   = local.server_id
  name        = "tf-acc-lookup"
  permissions = "8"
}
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-lookup"
  topic     = "lookup"
}
`
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.config(resources)},
			{
				Config: env.config(resources + `
data "discord_server" "test" {
  id = local.server_id
}
data "discord_role" "by_name" {
  server_id = local.server_id
  name      = discord_role.test.name
}
data "discord_role" "by_id" {
  server_id = local.server_id
  id        = discord_role.test.id
}
data "discord_role" "everyone" {
  server_id = local.server_id
  id        = local.server_id
}
data "discord_channel" "by_name" {
  server_id = local.server_id
  name      = discord_text_channel.test.name
  type      = "text"
}
data "discord_channel" "by_id" {
  server_id = local.server_id
  id        = discord_text_channel.test.id
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.discord_server.test", "name"),
					resource.TestCheckResourceAttrSet("data.discord_server.test", "owner_id"),
					resource.TestCheckResourceAttrPair("data.discord_role.by_name", "id", "discord_role.test", "id"),
					resource.TestCheckResourceAttr("data.discord_role.by_id", "permissions", "8"),
					resource.TestCheckResourceAttr("data.discord_role.everyone", "name", "@everyone"),
					resource.TestCheckResourceAttrPair("data.discord_channel.by_name", "id", "discord_text_channel.test", "id"),
					resource.TestCheckResourceAttr("data.discord_channel.by_id", "type", "text"),
					resource.TestCheckResourceAttr("data.discord_channel.by_id", "topic", "lookup"),
				),
			},
		},
	})
}

func TestAccDataSourceErrors(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.config(`
data "discord_role" "test" {
  server_id = local.server_id
  name      = "does-not-exist-anywhere"
}`),
				ExpectError: regexp.MustCompile(`No role found`),
			},
			{
				Config: env.config(`
data "discord_role" "test" {
  server_id = local.server_id
}`),
				ExpectError: regexp.MustCompile(`Exactly one of these attributes must be configured`),
			},
			{
				Config: env.config(`
data "discord_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-missing"
  id        = "123"
}`),
				ExpectError: regexp.MustCompile(`Invalid Attribute Combination`),
			},
		},
	})
}

func TestAccDataSourceDuplicateNames(t *testing.T) {
	env := newTestEnv(t)
	cfg := env.config(`
resource "discord_role" "a" {
  server_id = local.server_id
  name      = "tf-acc-dup"
}
resource "discord_role" "b" {
  server_id = local.server_id
  name      = "tf-acc-dup"
}
`)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				Config: cfg + `
data "discord_role" "test" {
  server_id = local.server_id
  name      = "tf-acc-dup"
}`,
				ExpectError: regexp.MustCompile(`Multiple roles found`),
			},
		},
	})
}

func TestAccMemberDataSourceByUsername(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(`
data "discord_member" "test" {
  server_id = local.server_id
  username  = "Tester"
}`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.discord_member.test", "user_id", env.userID),
				resource.TestCheckResourceAttr("data.discord_member.test", "username", "tester"),
				resource.TestCheckResourceAttr("data.discord_member.test", "bot", "false"),
			),
		}},
	})
}

func TestFunctions(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: `
output "perms" { value = provider::discord::permissions(["view_channel", "SEND_MESSAGES"]) }
output "none"  { value = provider::discord::permissions([]) }
output "color" { value = provider::discord::color("#5865F2") }
output "short" { value = provider::discord::color("fff") }
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownOutputValue("perms", knownvalue.StringExact("3072")),
					statecheck.ExpectKnownOutputValue("none", knownvalue.StringExact("0")),
					statecheck.ExpectKnownOutputValue("color", knownvalue.Int64Exact(5793266)),
					statecheck.ExpectKnownOutputValue("short", knownvalue.Int64Exact(16777215)),
				},
			},
			{
				Config:      `output "x" { value = provider::discord::permissions(["NOPE"]) }`,
				ExpectError: regexp.MustCompile(`unknown permission "NOPE"`),
			},
			{
				Config:      `output "x" { value = provider::discord::color("#12345") }`,
				ExpectError: regexp.MustCompile(`invalid hex color`),
			},
			{
				Config:      `output "x" { value = provider::discord::color("zzzzzz") }`,
				ExpectError: regexp.MustCompile(`invalid hex color`),
			},
		},
	})
}
