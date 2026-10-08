data "discord_member" "alice" {
  server_id = var.server_id
  username  = "alice"
}

# Alice has exactly these roles. Any other role, apart from managed roles such
# as Server Booster, is removed on the next apply.
resource "discord_member_roles" "alice" {
  server_id = var.server_id
  user_id   = data.discord_member.alice.user_id
  role_ids = [
    discord_role.member.id,
    discord_role.moderator.id,
  ]

  audit_log_reason = "Moderator roster (OPS-42)"
}
