# On Terraform 1.8 and later, prefer provider::discord::permissions().
data "discord_permissions" "read_only" {
  allow = ["VIEW_CHANNEL", "READ_MESSAGE_HISTORY"]
  deny  = ["SEND_MESSAGES"]
}

resource "discord_channel_permission" "read_only" {
  channel_id   = discord_text_channel.announcements.id
  overwrite_id = var.server_id
  type         = "role"
  allow        = data.discord_permissions.read_only.allow_bits
  deny         = data.discord_permissions.read_only.deny_bits
}
