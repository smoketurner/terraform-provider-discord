resource "discord_soundboard_sound" "quack" {
  server_id  = var.server_id
  name       = "quack"
  sound      = "data:audio/mpeg;base64,${filebase64("${path.module}/quack.mp3")}"
  volume     = 0.5
  emoji_name = "🦆"
}

# With Terraform 1.11 or later, sound_wo keeps the sound out of state. Bump
# sound_wo_version to upload a new sound, which creates a new soundboard sound.
resource "discord_soundboard_sound" "cheer" {
  server_id        = var.server_id
  name             = "cheer"
  sound_wo         = "data:audio/ogg;base64,${filebase64("${path.module}/cheer.ogg")}"
  sound_wo_version = 1
  emoji_id         = discord_emoji.party.id
}
