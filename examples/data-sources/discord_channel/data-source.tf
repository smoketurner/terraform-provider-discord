data "discord_channel" "general" {
  server_id = var.server_id
  name      = "general"
  type      = "text"
}
