# Run with: terraform apply -invoke=action.discord_prune_members.inactive
action "discord_prune_members" "inactive" {
  config {
    server_id        = var.server_id
    days             = 30
    include_role_ids = [discord_role.visitor.id]
    dry_run          = true # report the count without removing anyone
    audit_log_reason = "Inactive for 30 days"
  }
}
