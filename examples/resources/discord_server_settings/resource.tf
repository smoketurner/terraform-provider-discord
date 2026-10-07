resource "discord_server_settings" "main" {
  server_id                     = var.server_id
  name                          = "My Community"
  verification_level            = "medium"
  default_message_notifications = "only_mentions"
  explicit_content_filter       = "all_members"
  system_channel_id             = discord_text_channel.general.id
  icon                          = "data:image/png;base64,${filebase64("${path.module}/icon.png")}"
}
