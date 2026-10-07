resource "discord_role" "moderator" {
  server_id   = var.server_id
  name        = "Moderator"
  permissions = provider::discord::permissions(["KICK_MEMBERS", "MANAGE_MESSAGES", "MODERATE_MEMBERS"])
}
