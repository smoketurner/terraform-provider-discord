# Run with: terraform apply -invoke=action.discord_set_voice_channel_status.raid_night
action "discord_set_voice_channel_status" "raid_night" {
  config {
    channel_id = discord_voice_channel.lobby.id
    status     = "Raid night: 8pm UTC"
  }
}
