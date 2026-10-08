data "discord_current_user" "bot" {}

# Let the bot itself post in the staff channel hidden from @everyone.
resource "discord_channel_permission" "staff_bot" {
  channel_id   = discord_text_channel.staff.id
  overwrite_id = data.discord_current_user.bot.id
  type         = "member"
  allow        = provider::discord::permissions(["VIEW_CHANNEL", "SEND_MESSAGES"])
}
