data "discord_role" "moderators" {
  server_id = var.server_id
  name      = "Moderators"
}

# What the role grants across the server, together with @everyone.
data "discord_role_permissions" "moderators" {
  server_id = var.server_id
  role_id   = data.discord_role.moderators.id
}

# What the role grants in one channel, after the channel's overwrites for
# @everyone and the role. implicit = false keeps permissions that Discord
# would otherwise mask because the role cannot view the channel.
data "discord_role_permissions" "moderators_staff" {
  server_id  = var.server_id
  role_id    = data.discord_role.moderators.id
  channel_id = var.staff_channel_id
  implicit   = false
}

output "moderator_permissions" {
  value = data.discord_role_permissions.moderators.permission_names
}
