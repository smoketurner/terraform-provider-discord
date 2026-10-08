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
