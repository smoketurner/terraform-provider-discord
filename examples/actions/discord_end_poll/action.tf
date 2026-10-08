# Run with: terraform apply -invoke=action.discord_end_poll.lunch
action "discord_end_poll" "lunch" {
  config {
    channel_id = discord_text_channel.general.id
    message_id = "345678901234567890"
  }
}
