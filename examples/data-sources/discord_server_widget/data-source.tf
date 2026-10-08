resource "discord_server_widget" "main" {
  server_id = var.server_id
  enabled   = true
}

data "discord_server_widget" "main" {
  server_id  = var.server_id
  depends_on = [discord_server_widget.main]
}

output "online_members" {
  value = data.discord_server_widget.main.presence_count
}
