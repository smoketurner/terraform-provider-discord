data "discord_role" "moderators" {
  server_id = var.server_id
  name      = "Moderators"
}

# What the role grants in a channel, after the channel's overwrites for
# @everyone and the role. implicit = false keeps the permissions Discord
# would otherwise mask because the role cannot view the channel.
data "discord_channel_role_permissions" "moderators_staff" {
  channel_id = var.staff_channel_id
  role_id    = data.discord_role.moderators.id
  implicit   = false
}
