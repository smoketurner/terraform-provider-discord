resource "discord_server_template" "community" {
  server_id = var.server_id
  name      = "Community"
}

action "discord_sync_server_template" "community" {
  config {
    server_id = var.server_id
    code      = discord_server_template.community.code
  }
}

# Syncs the template after the channels it captures change.
resource "terraform_data" "channels" {
  input = [discord_text_channel.general.id, discord_text_channel.rules.id]

  lifecycle {
    action_trigger {
      events  = [after_update]
      actions = [action.discord_sync_server_template.community]
    }
  }
}
