data "discord_invite" "partner" {
  code = var.invite_code
}

output "partner_server" {
  value = "${data.discord_invite.partner.server_name} (${data.discord_invite.partner.approximate_member_count} members)"
}
