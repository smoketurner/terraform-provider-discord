# Requires the server to have Community enabled.
resource "discord_announcement_channel" "news" {
  server_id   = var.server_id
  name        = "announcements"
  category_id = discord_category_channel.community.id
  topic       = "Release notes and news"
}
