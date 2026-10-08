# Roles created by integrations or by hand can be referenced by name.
data "discord_role" "server_booster" {
  server_id = var.server_id
  name      = "Server Booster"
}
