data "discord_member" "bob" {
  server_id = var.server_id
  username  = "bob"
}

resource "discord_member" "bob" {
  server_id = var.server_id
  user_id   = data.discord_member.bob.user_id
  nick      = "Bob (Support)"

  # Times Bob out until this moment, which must be at most 28 days ahead when
  # applied. Once it has passed, the expired timeout is not a change.
  communication_disabled_until = "2026-11-01T00:00:00Z"

  audit_log_reason = "Repeated spam (MOD-7)"
}
