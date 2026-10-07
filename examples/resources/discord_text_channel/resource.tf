resource "discord_text_channel" "general" {
  server_id           = var.server_id
  name                = "general"
  category_id         = discord_category_channel.community.id
  topic               = "Chat about anything"
  rate_limit_per_user = 5
}
