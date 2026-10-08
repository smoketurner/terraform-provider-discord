resource "discord_message" "release_notes" {
  channel_id = discord_announcement_channel.news.id
  content    = "Version 1.2.3 is out."

  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.discord_crosspost_message.release_notes]
    }
  }
}

action "discord_crosspost_message" "release_notes" {
  config {
    channel_id = discord_announcement_channel.news.id
    message_id = discord_message.release_notes.id
  }
}
