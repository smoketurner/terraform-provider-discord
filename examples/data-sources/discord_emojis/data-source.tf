data "discord_emojis" "all" {
  server_id = var.server_id
}

output "emoji_ids" {
  value = { for e in data.discord_emojis.all.emojis : e.name => e.id }
}
