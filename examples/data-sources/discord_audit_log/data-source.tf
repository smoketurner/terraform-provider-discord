# The 50 most recent bans (action type 22) and their reasons.
data "discord_audit_log" "bans" {
  server_id   = var.server_id
  action_type = 22
}

output "ban_reasons" {
  value = { for e in data.discord_audit_log.bans.entries : e.target_id => e.reason }
}

# Changes to a role made outside Terraform, with old and new values decoded.
data "discord_audit_log" "role_updates" {
  server_id   = var.server_id
  action_type = 31
  limit       = 200
}

output "role_changes" {
  value = [
    for e in data.discord_audit_log.role_updates.entries : {
      by      = e.user_id
      changes = { for c in e.changes : c.key => c.new_value == null ? null : jsondecode(c.new_value) }
    }
  ]
}
