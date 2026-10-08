resource "discord_ban" "spammer" {
  server_id              = var.server_id
  user_id                = "456789012345678901"
  reason                 = "Spam"
  delete_message_seconds = 86400
}
