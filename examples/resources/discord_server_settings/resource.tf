resource "discord_server_settings" "main" {
  server_id                     = var.server_id
  name                          = "My Community"
  verification_level            = "medium"
  default_message_notifications = "only_mentions"
  explicit_content_filter       = "all_members"
  system_channel_id             = discord_text_channel.general.id
  icon                          = "data:image/png;base64,${filebase64("${path.module}/icon.png")}"

  # Community needs a rules channel and a public updates channel.
  community                 = true
  rules_channel_id          = discord_text_channel.rules.id
  public_updates_channel_id = discord_text_channel.moderators.id

  # "" clears a setting; omitting it leaves the setting unmanaged.
  afk_channel_id = ""

  # Requires the BANNER feature. On Terraform 1.11 or later, banner_wo keeps
  # the image out of state.
  banner_wo         = "data:image/png;base64,${filebase64("${path.module}/banner.png")}"
  banner_wo_version = 1
}
