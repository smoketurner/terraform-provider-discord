data "discord_entitlement" "purchase" {
  id = var.entitlement_id
}

output "purchase_active" {
  value = !data.discord_entitlement.purchase.deleted && data.discord_entitlement.purchase.ends_at == null
}
