data "discord_current_user" "bot" {}

# The bot's permissions in a channel, after the channel's overwrites.
data "discord_channel_member_permissions" "bot_announcements" {
  channel_id = var.announcements_channel_id
  user_id    = data.discord_current_user.bot.id
}

check "bot_can_post_announcements" {
  assert {
    condition     = contains(data.discord_channel_member_permissions.bot_announcements.permission_names, "SEND_MESSAGES")
    error_message = "The bot cannot send messages in the announcements channel."
  }
}
