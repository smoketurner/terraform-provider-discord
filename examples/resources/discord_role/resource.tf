resource "discord_role" "member" {
  server_id   = var.server_id
  name        = "Member"
  color       = provider::discord::color("#57F287")
  hoist       = true
  mentionable = false
  permissions = provider::discord::permissions([
    "VIEW_CHANNEL",
    "SEND_MESSAGES",
    "READ_MESSAGE_HISTORY",
    "ADD_REACTIONS",
    "CONNECT",
    "SPEAK",
  ])

  # Overrides the provider's audit_log_reason for this role.
  audit_log_reason = "Default member role (OPS-42)"
}

# Role icons require the server to have the ROLE_ICONS feature. With
# Terraform 1.11 or later, icon_wo keeps the image out of state. Bump
# icon_wo_version to upload a new icon.
resource "discord_role" "moderator" {
  server_id       = var.server_id
  name            = "Moderator"
  icon_wo         = "data:image/png;base64,${filebase64("${path.module}/moderator.png")}"
  icon_wo_version = 1
}
