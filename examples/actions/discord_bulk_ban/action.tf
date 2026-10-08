# Run with: terraform apply -invoke=action.discord_bulk_ban.raid
action "discord_bulk_ban" "raid" {
  config {
    server_id              = var.server_id
    user_ids               = ["456789012345678901", "567890123456789012"]
    delete_message_seconds = 3600
    audit_log_reason       = "Raid"
  }
}
