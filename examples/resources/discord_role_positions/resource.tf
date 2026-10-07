# Listed from highest to lowest, as in the Discord client.
resource "discord_role_positions" "main" {
  server_id = var.server_id
  role_ids = [
    discord_role.admin.id,
    discord_role.moderator.id,
    discord_role.member.id,
  ]
}
