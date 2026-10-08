# Opens the stage for the community call.
resource "discord_stage_instance" "community_call" {
  channel_id              = discord_stage_channel.town_hall.id
  topic                   = "Community call"
  scheduled_event_id      = discord_scheduled_event.community_call.id
  send_start_notification = true
}
