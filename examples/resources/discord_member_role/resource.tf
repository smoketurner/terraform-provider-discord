data "discord_member" "alice" {
  server_id = var.server_id
  username  = "alice"
}

resource "discord_member_role" "alice_moderator" {
  server_id = var.server_id
  user_id   = data.discord_member.alice.user_id
  role_id   = discord_role.moderator.id
}
