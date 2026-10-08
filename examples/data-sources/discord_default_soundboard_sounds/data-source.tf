data "discord_default_soundboard_sounds" "all" {}

output "default_sound_names" {
  value = data.discord_default_soundboard_sounds.all.sounds[*].name
}
