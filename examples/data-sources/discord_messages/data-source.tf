# The 20 newest messages of a channel.
data "discord_messages" "recent" {
  channel_id = var.channel_id
  limit      = 20
}

# The messages around a message, including it.
data "discord_messages" "context" {
  channel_id = var.channel_id
  around     = var.message_id
  limit      = 5
}

output "recent_authors" {
  value = toset(data.discord_messages.recent.messages[*].author_id)
}
