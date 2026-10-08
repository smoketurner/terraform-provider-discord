resource "discord_media_channel" "showcase" {
  server_id                   = var.server_id
  name                        = "showcase"
  category_id                 = discord_category_channel.community.id
  topic                       = "Share screenshots and clips of your builds."
  require_tag                 = true
  hide_media_download_options = true
  default_sort_order          = "creation_date"

  default_reaction_emoji = {
    emoji_name = "🔥"
  }

  available_tags = [
    { name = "screenshot", emoji_name = "📸" },
    { name = "video", emoji_name = "🎬" },
    { name = "featured", moderated = true },
  ]
}
