# Works for discoverable servers the bot is not a member of.
data "discord_server_preview" "partner" {
  id = var.partner_server_id
}
