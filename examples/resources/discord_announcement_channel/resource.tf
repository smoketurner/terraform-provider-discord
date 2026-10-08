# Requires the server to have Community enabled.
resource "discord_announcement_channel" "news" {
  server_id   = var.server_id
  name        = "announcements"
  category_id = discord_category_channel.community.id
  topic       = "Release notes and news"
}

# Converts a channel previously managed as discord_text_channel.news in place,
# keeping its messages, instead of recreating it (Terraform 1.8 or later).
moved {
  from = discord_text_channel.news
  to   = discord_announcement_channel.news
}
