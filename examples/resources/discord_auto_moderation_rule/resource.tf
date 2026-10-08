resource "discord_auto_moderation_rule" "scams" {
  server_id    = var.server_id
  name         = "Block scam links"
  event_type   = "message_send"
  trigger_type = "keyword"
  enabled      = true

  trigger_metadata = {
    keyword_filter = ["free nitro", "*giveaway*"]
    regex_patterns = ["disc[o0]rd-?gift"]
    allow_list     = ["giveaway-winners"]
  }

  actions = [
    { type = "block_message", custom_message = "Scam links are not allowed." },
    { type = "send_alert_message", channel_id = var.mod_log_channel_id },
    { type = "timeout", duration_seconds = 3600 },
  ]

  exempt_role_ids = [var.moderator_role_id]
}

resource "discord_auto_moderation_rule" "presets" {
  server_id    = var.server_id
  name         = "Block slurs and sexual content"
  event_type   = "message_send"
  trigger_type = "keyword_preset"
  enabled      = true

  trigger_metadata = {
    presets = ["sexual_content", "slurs"]
  }

  actions = [{ type = "block_message" }]
}

resource "discord_auto_moderation_rule" "profiles" {
  server_id    = var.server_id
  name         = "Quarantine impersonators"
  event_type   = "member_update"
  trigger_type = "member_profile"
  enabled      = true

  trigger_metadata = {
    keyword_filter = ["*moderator*", "*admin*"]
  }

  actions = [{ type = "block_member_interaction" }]
}
