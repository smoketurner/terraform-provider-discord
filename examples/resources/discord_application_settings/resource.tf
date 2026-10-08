resource "discord_application_settings" "this" {
  description = "Moderation and welcome messages for our community."
  tags        = ["moderation", "utility"]

  integration_types_config = {
    guild_install = {
      scopes      = ["applications.commands", "bot"]
      permissions = provider::discord::permissions(["SEND_MESSAGES", "MANAGE_MESSAGES"])
    }
    user_install = {}
  }

  role_connections_verification_url = "https://example.com/discord/linked-roles"
  gateway_message_content_limited   = true

  icon_wo         = "data:image/png;base64,${filebase64("${path.module}/icon.png")}"
  icon_wo_version = 1
}
