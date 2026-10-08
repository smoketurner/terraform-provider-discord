# Pause invites for 12 hours from the first apply. A fixed timestamp keeps
# the plan stable, unlike timestamp(), which changes on every run. Replace
# the time_offset to start a new pause.
resource "time_offset" "raid" {
  offset_hours = 12
}

resource "discord_server_incident_actions" "main" {
  server_id              = var.server_id
  invites_disabled_until = time_offset.raid.rfc3339
}
