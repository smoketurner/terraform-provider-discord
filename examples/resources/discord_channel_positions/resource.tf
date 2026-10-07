# Listed from top to bottom, as in the Discord client.
resource "discord_channel_positions" "community" {
  server_id = var.server_id
  channel_ids = [
    discord_text_channel.general.id,
    discord_forum_channel.help.id,
    discord_voice_channel.lounge.id,
  ]
}
