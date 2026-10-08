# Run with: terraform apply -invoke=action.discord_bulk_delete_messages.spam
action "discord_bulk_delete_messages" "spam" {
  config {
    channel_id       = discord_text_channel.general.id
    message_ids      = ["345678901234567890", "345678901234567891"]
    audit_log_reason = "Spam"
  }
}
