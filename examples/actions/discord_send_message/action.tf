action "discord_send_message" "deployed" {
  config {
    channel_id = discord_text_channel.deployments.id
    content    = "Deployed ${var.app_version}"
  }
}

# Posts the message each time the version changes.
resource "terraform_data" "release" {
  input = var.app_version

  lifecycle {
    action_trigger {
      events  = [after_create, after_update]
      actions = [action.discord_send_message.deployed]
    }
  }
}
