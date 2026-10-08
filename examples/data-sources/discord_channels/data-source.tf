# Let moderators read and send messages in every text channel of a category.
data "discord_channels" "staff" {
  server_id   = var.server_id
  type        = "text"
  category_id = var.staff_category_id
}

resource "discord_channel_permission" "moderators" {
  for_each = toset([for c in data.discord_channels.staff.channels : c.id])

  channel_id   = each.value
  type         = "role"
  overwrite_id = var.moderator_role_id
  allow        = provider::discord::permissions(["VIEW_CHANNEL", "SEND_MESSAGES"])
}
