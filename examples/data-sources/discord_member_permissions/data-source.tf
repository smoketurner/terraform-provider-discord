data "discord_current_user" "bot" {}

# The bot's permissions across the server.
data "discord_member_permissions" "bot" {
  server_id = var.server_id
  user_id   = data.discord_current_user.bot.id
}

# The bot's permissions in one channel, after its permission overwrites.
data "discord_member_permissions" "bot_announcements" {
  server_id  = var.server_id
  user_id    = data.discord_current_user.bot.id
  channel_id = var.announcements_channel_id
}

check "bot_can_post_announcements" {
  assert {
    condition     = contains(data.discord_member_permissions.bot_announcements.permission_names, "SEND_MESSAGES")
    error_message = "The bot cannot send messages in the announcements channel."
  }
}
