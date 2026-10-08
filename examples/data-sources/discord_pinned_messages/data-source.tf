data "discord_pinned_messages" "announcements" {
  channel_id = var.channel_id
}

output "pinned_message_ids" {
  value = data.discord_pinned_messages.announcements.messages[*].id
}
