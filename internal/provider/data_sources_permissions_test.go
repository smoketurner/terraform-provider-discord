package provider

import (
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

func allPermissionBits() string {
	var all uint64
	for _, bit := range discord.Permissions {
		all |= bit
	}
	return strconv.FormatUint(all, 10)
}

// permissionRoles makes @everyone grant VIEW_CHANNEL and SEND_MESSAGES only,
// and creates a speaker role granting ADD_REACTIONS, a moderator role
// granting KICK_MEMBERS and an administrator role.
const permissionRoles = `
resource "discord_role_everyone" "test" {
  server_id   = local.server_id
  permissions = provider::discord::permissions(["VIEW_CHANNEL", "SEND_MESSAGES"])
}
resource "discord_role" "speaker" {
  server_id   = local.server_id
  name        = "tf-acc-speaker"
  permissions = provider::discord::permissions(["ADD_REACTIONS"])
}
resource "discord_role" "moderator" {
  server_id   = local.server_id
  name        = "tf-acc-moderator"
  permissions = provider::discord::permissions(["KICK_MEMBERS"])
}
resource "discord_role" "admin" {
  server_id   = local.server_id
  name        = "tf-acc-admin"
  permissions = provider::discord::permissions(["ADMINISTRATOR"])
}
`

func TestAccPermissionsDataSourcesServer(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	alice := env.fake.AddMember(env.serverID, "alice")
	bob := env.fake.AddMember(env.serverID, "bob")
	config := permissionRoles + fmt.Sprintf(`
resource "discord_member_role" "alice_speaker" {
  server_id = local.server_id
  user_id   = %[1]q
  role_id   = discord_role.speaker.id
}
resource "discord_member_role" "alice_moderator" {
  server_id = local.server_id
  user_id   = %[1]q
  role_id   = discord_role.moderator.id
}
resource "discord_member_role" "bob_admin" {
  server_id = local.server_id
  user_id   = %[2]q
  role_id   = discord_role.admin.id
}
data "discord_server_member_permissions" "alice" {
  server_id  = local.server_id
  user_id    = %[1]q
  depends_on = [discord_role_everyone.test, discord_member_role.alice_speaker, discord_member_role.alice_moderator]
}
data "discord_server_role_permissions" "speaker" {
  server_id  = local.server_id
  role_id    = discord_role.speaker.id
  depends_on = [discord_role_everyone.test]
}
data "discord_server_member_permissions" "admin" {
  server_id  = local.server_id
  user_id    = %[2]q
  depends_on = [discord_member_role.bob_admin]
}
data "discord_server_member_permissions" "owner" {
  server_id = local.server_id
  user_id   = %[3]q
}
`, alice, bob, env.userID)
	all := allPermissionBits()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(config),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.discord_server_member_permissions.alice", "permissions", "3138"),
				resource.TestCheckResourceAttr("data.discord_server_member_permissions.alice", "permission_names.#", "4"),
				resource.TestCheckTypeSetElemAttr("data.discord_server_member_permissions.alice", "permission_names.*", "ADD_REACTIONS"),
				resource.TestCheckTypeSetElemAttr("data.discord_server_member_permissions.alice", "permission_names.*", "KICK_MEMBERS"),
				resource.TestCheckTypeSetElemAttr("data.discord_server_member_permissions.alice", "permission_names.*", "SEND_MESSAGES"),
				resource.TestCheckTypeSetElemAttr("data.discord_server_member_permissions.alice", "permission_names.*", "VIEW_CHANNEL"),
				resource.TestCheckResourceAttr("data.discord_server_role_permissions.speaker", "permissions", "3136"),
				resource.TestCheckResourceAttr("data.discord_server_role_permissions.speaker", "permission_names.#", "3"),
				resource.TestCheckResourceAttr("data.discord_server_member_permissions.admin", "permissions", all),
				resource.TestCheckResourceAttr("data.discord_server_member_permissions.owner", "permissions", all),
			),
		}},
	})
}

func TestAccPermissionsDataSourcesChannel(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	alice := env.fake.AddMember(env.serverID, "alice")
	carol := env.fake.AddMember(env.serverID, "carol")
	dave := env.fake.AddMember(env.serverID, "dave")
	// In the text channel @everyone loses SEND_MESSAGES, the speaker role
	// gets it back even though carol's moderator role denies it, and alice
	// loses it again with a member overwrite. The
	// private channel hides itself from @everyone, and the voice channel
	// has no CONNECT.
	config := permissionRoles + fmt.Sprintf(`
locals {
  alice = %[1]q
  carol = %[2]q
  dave  = %[3]q
}
resource "discord_member_role" "alice" {
  server_id = local.server_id
  user_id   = local.alice
  role_id   = discord_role.speaker.id
}
resource "discord_member_role" "carol" {
  server_id = local.server_id
  user_id   = local.carol
  role_id   = discord_role.speaker.id
}
resource "discord_member_role" "carol_moderator" {
  server_id = local.server_id
  user_id   = local.carol
  role_id   = discord_role.moderator.id
}
resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-permissions"
}
resource "discord_channel_permission" "everyone" {
  channel_id   = discord_text_channel.test.id
  overwrite_id = local.server_id
  type         = "role"
  deny         = provider::discord::permissions(["SEND_MESSAGES"])
}
resource "discord_channel_permission" "speaker" {
  channel_id   = discord_text_channel.test.id
  overwrite_id = discord_role.speaker.id
  type         = "role"
  allow        = provider::discord::permissions(["SEND_MESSAGES"])
}
resource "discord_channel_permission" "moderator" {
  channel_id   = discord_text_channel.test.id
  overwrite_id = discord_role.moderator.id
  type         = "role"
  deny         = provider::discord::permissions(["SEND_MESSAGES"])
}
resource "discord_channel_permission" "alice" {
  channel_id   = discord_text_channel.test.id
  overwrite_id = local.alice
  type         = "member"
  deny         = provider::discord::permissions(["SEND_MESSAGES"])
}
resource "discord_thread" "test" {
  channel_id = discord_text_channel.test.id
  name       = "tf-acc-permissions-thread"
}
resource "discord_text_channel" "private" {
  server_id = local.server_id
  name      = "tf-acc-permissions-private"
}
resource "discord_channel_permission" "private" {
  channel_id   = discord_text_channel.private.id
  overwrite_id = local.server_id
  type         = "role"
  deny         = provider::discord::permissions(["VIEW_CHANNEL"])
}
resource "discord_voice_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-permissions-voice"
}
data "discord_current_user" "bot" {}

data "discord_channel_member_permissions" "alice" {
  user_id    = local.alice
  channel_id = discord_text_channel.test.id
  depends_on = [discord_role_everyone.test, discord_member_role.alice, discord_member_role.carol, discord_member_role.carol_moderator, discord_channel_permission.everyone, discord_channel_permission.speaker, discord_channel_permission.moderator, discord_channel_permission.alice]
}
data "discord_channel_member_permissions" "carol" {
  user_id    = local.carol
  channel_id = discord_text_channel.test.id
  depends_on = [discord_role_everyone.test, discord_member_role.alice, discord_member_role.carol, discord_member_role.carol_moderator, discord_channel_permission.everyone, discord_channel_permission.speaker, discord_channel_permission.moderator, discord_channel_permission.alice]
}
data "discord_channel_member_permissions" "dave" {
  user_id    = local.dave
  channel_id = discord_text_channel.test.id
  depends_on = [discord_role_everyone.test, discord_member_role.alice, discord_member_role.carol, discord_member_role.carol_moderator, discord_channel_permission.everyone, discord_channel_permission.speaker, discord_channel_permission.moderator, discord_channel_permission.alice]
}
data "discord_channel_role_permissions" "speaker" {
  role_id    = discord_role.speaker.id
  channel_id = discord_text_channel.test.id
  depends_on = [discord_role_everyone.test, discord_member_role.alice, discord_member_role.carol, discord_member_role.carol_moderator, discord_channel_permission.everyone, discord_channel_permission.speaker, discord_channel_permission.moderator, discord_channel_permission.alice]
}
data "discord_channel_member_permissions" "carol_thread" {
  user_id    = local.carol
  channel_id = discord_thread.test.id
  depends_on = [discord_role_everyone.test, discord_member_role.alice, discord_member_role.carol, discord_member_role.carol_moderator, discord_channel_permission.everyone, discord_channel_permission.speaker, discord_channel_permission.moderator, discord_channel_permission.alice]
}
data "discord_channel_member_permissions" "alice_thread" {
  user_id    = local.alice
  channel_id = discord_thread.test.id
  depends_on = [discord_role_everyone.test, discord_member_role.alice, discord_member_role.carol, discord_member_role.carol_moderator, discord_channel_permission.everyone, discord_channel_permission.speaker, discord_channel_permission.moderator, discord_channel_permission.alice]
}
data "discord_channel_member_permissions" "dave_private" {
  user_id    = local.dave
  channel_id = discord_text_channel.private.id
  depends_on = [discord_role_everyone.test, discord_channel_permission.private]
}
data "discord_channel_member_permissions" "dave_private_explicit" {
  user_id    = local.dave
  channel_id = discord_text_channel.private.id
  implicit   = false
  depends_on = [discord_role_everyone.test, discord_channel_permission.private]
}
data "discord_channel_role_permissions" "everyone_private" {
  role_id    = local.server_id
  channel_id = discord_text_channel.private.id
  depends_on = [discord_role_everyone.test, discord_channel_permission.private]
}
data "discord_channel_member_permissions" "bot_private" {
  user_id    = data.discord_current_user.bot.id
  channel_id = discord_text_channel.private.id
  depends_on = [discord_role_everyone.test, discord_channel_permission.private]
}
data "discord_channel_member_permissions" "dave_voice" {
  user_id    = local.dave
  channel_id = discord_voice_channel.test.id
  depends_on = [discord_role_everyone.test]
}
data "discord_channel_member_permissions" "dave_voice_explicit" {
  user_id    = local.dave
  channel_id = discord_voice_channel.test.id
  implicit   = false
  depends_on = [discord_role_everyone.test]
}
`, alice, carol, dave)
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.config(config),
			Check: resource.ComposeAggregateTestCheckFunc(
				// VIEW_CHANNEL | ADD_REACTIONS; the member overwrite wins
				// over the role's SEND_MESSAGES.
				resource.TestCheckResourceAttr("data.discord_channel_member_permissions.alice", "permissions", "1088"),
				// The speaker allow wins over the @everyone deny and over the
				// moderator deny.
				resource.TestCheckResourceAttr("data.discord_channel_member_permissions.carol", "permissions", "3138"),
				resource.TestCheckResourceAttr("data.discord_channel_member_permissions.dave", "permissions", "1024"),
				// The role endpoint ignores alice's member overwrite.
				resource.TestCheckResourceAttr("data.discord_channel_role_permissions.speaker", "permissions", "3136"),
				// Threads use their parent's overwrites.
				resource.TestCheckResourceAttr("data.discord_channel_member_permissions.carol_thread", "permissions", "3138"),
				resource.TestCheckResourceAttr("data.discord_channel_member_permissions.alice_thread", "permissions", "1088"),
				// Without VIEW_CHANNEL everything is masked unless implicit
				// is false or the member is a bot.
				resource.TestCheckResourceAttr("data.discord_channel_member_permissions.dave_private", "permissions", "0"),
				resource.TestCheckResourceAttr("data.discord_channel_member_permissions.dave_private", "permission_names.#", "0"),
				resource.TestCheckResourceAttr("data.discord_channel_member_permissions.dave_private_explicit", "permissions", "2048"),
				resource.TestCheckResourceAttr("data.discord_channel_role_permissions.everyone_private", "permissions", "0"),
				resource.TestCheckResourceAttr("data.discord_channel_member_permissions.bot_private", "permissions", "2048"),
				// Without CONNECT a voice channel keeps only VIEW_CHANNEL.
				resource.TestCheckResourceAttr("data.discord_channel_member_permissions.dave_voice", "permissions", "1024"),
				resource.TestCheckResourceAttr("data.discord_channel_member_permissions.dave_voice_explicit", "permissions", "3072"),
			),
		}},
	})
}

func TestAccPermissionsDataSourcesErrors(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	const unknown = "999999999999999999"
	step := func(hcl, want string) resource.TestStep {
		return resource.TestStep{Config: env.config(hcl), ExpectError: regexp.MustCompile(want)}
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			step(fmt.Sprintf(`data "discord_server_member_permissions" "x" {
  server_id = local.server_id
  user_id   = %q
}`, unknown), `(?s)read server member permissions.*Unknown Member`),
			step(fmt.Sprintf(`data "discord_server_role_permissions" "x" {
  server_id = local.server_id
  role_id   = %q
}`, unknown), `(?s)read server role permissions.*Unknown Role`),
			step(fmt.Sprintf(`data "discord_channel_member_permissions" "x" {
  channel_id = %q
  user_id    = %q
}`, unknown, env.userID), `(?s)read channel member permissions.*Unknown Channel`),
			step(fmt.Sprintf(`resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-permissions-errors"
}
data "discord_channel_member_permissions" "x" {
  channel_id = discord_text_channel.test.id
  user_id    = %q
}`, unknown), `(?s)read channel member permissions.*Unknown Member`),
			step(fmt.Sprintf(`resource "discord_text_channel" "test" {
  server_id = local.server_id
  name      = "tf-acc-permissions-errors"
}
data "discord_channel_role_permissions" "x" {
  channel_id = discord_text_channel.test.id
  role_id    = %q
}`, unknown), `(?s)read channel role permissions.*Unknown Role`),
		},
	})
	// Validation fails before any request, so these run without resources
	// to destroy afterwards.
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			step(`data "discord_server_role_permissions" "x" {
  server_id = local.server_id
  role_id   = "everyone"
}`, `(?i)snowflake`),
			step(`data "discord_channel_role_permissions" "x" {
  channel_id = "general"
  role_id    = local.server_id
}`, `(?i)snowflake`),
		},
	})
}
