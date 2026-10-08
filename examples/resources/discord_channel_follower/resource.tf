# Post another server's release announcements into a channel of this server.
resource "discord_channel_follower" "releases" {
  source_channel_id = "890123456789012345"
  channel_id        = discord_text_channel.releases.id
  audit_log_reason  = "Follow upstream release notes"
}
