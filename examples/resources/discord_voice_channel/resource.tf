resource "discord_voice_channel" "lounge" {
  server_id          = var.server_id
  name               = "Lounge"
  category_id        = discord_category_channel.community.id
  user_limit         = 10
  video_quality_mode = "full"
}
