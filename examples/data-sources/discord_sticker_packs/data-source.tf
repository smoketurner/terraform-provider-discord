data "discord_sticker_packs" "all" {}

output "sticker_pack_names" {
  value = data.discord_sticker_packs.all.packs[*].name
}
