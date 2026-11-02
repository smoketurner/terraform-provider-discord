data "discord_role" "moderators" {
  server_id = var.server_id
  name      = "Moderators"
}

# What the role grants across the server, together with @everyone.
data "discord_server_role_permissions" "moderators" {
  server_id = var.server_id
  role_id   = data.discord_role.moderators.id
}

output "moderator_permissions" {
  value = data.discord_server_role_permissions.moderators.permission_names
}
