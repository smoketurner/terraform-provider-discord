resource "discord_forum_channel" "help" {
  server_id            = var.server_id
  name                 = "help"
  category_id          = discord_category_channel.community.id
  topic                = "Search before posting, and include your config."
  require_tag          = true
  default_sort_order   = "latest_activity"
  default_forum_layout = "list_view"

  default_reaction_emoji = {
    emoji_name = "✅"
  }

  available_tags = [
    { name = "question", emoji_name = "❓" },
    { name = "bug", emoji_name = "🐛" },
    { name = "solved", moderated = true },
  ]
}
