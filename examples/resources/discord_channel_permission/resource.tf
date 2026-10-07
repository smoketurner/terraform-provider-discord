# Hide the staff channel from @everyone (the role ID equals the server ID) and
# show it to moderators.
resource "discord_channel_permission" "staff_hidden" {
  channel_id   = discord_text_channel.staff.id
  overwrite_id = var.server_id
  type         = "role"
  deny         = provider::discord::permissions(["VIEW_CHANNEL"])
}

resource "discord_channel_permission" "staff_moderators" {
  channel_id   = discord_text_channel.staff.id
  overwrite_id = discord_role.moderator.id
  type         = "role"
  allow        = provider::discord::permissions(["VIEW_CHANNEL", "SEND_MESSAGES"])
}
