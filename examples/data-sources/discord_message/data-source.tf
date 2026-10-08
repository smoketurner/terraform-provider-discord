data "discord_message" "rules" {
  channel_id = var.channel_id
  id         = var.message_id
}
