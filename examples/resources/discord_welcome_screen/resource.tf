resource "discord_welcome_screen" "main" {
  server_id   = var.server_id
  enabled     = true
  description = "A place to talk about everything."

  welcome_channels = [
    {
      channel_id  = discord_text_channel.rules.id
      description = "Read the rules first"
      emoji_name  = "📜"
    },
    {
      channel_id  = discord_text_channel.general.id
      description = "Say hello"
      emoji_id    = discord_emoji.wave.id
    },
  ]
}
