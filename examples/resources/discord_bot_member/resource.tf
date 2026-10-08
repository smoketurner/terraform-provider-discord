resource "discord_bot_member" "this" {
  server_id = var.server_id
  nick      = "Helper (beta)"
  bio       = "Ask me about the server rules."

  banner_wo         = "data:image/png;base64,${filebase64("${path.module}/banner.png")}"
  banner_wo_version = 1
}
