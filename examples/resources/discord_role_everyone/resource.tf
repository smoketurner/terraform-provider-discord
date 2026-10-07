# Lock the server down: @everyone can only see the welcome channel, which grants
# access through its own permission overwrite.
resource "discord_role_everyone" "everyone" {
  server_id   = var.server_id
  permissions = provider::discord::permissions(["READ_MESSAGE_HISTORY"])
}
