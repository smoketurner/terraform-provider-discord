# Terraform Provider for Discord

Manage Discord servers as code with Terraform: server settings, roles, channels, permission overwrites, member roles,
webhooks, invites, messages and custom emojis.

Built on the [Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework) and published to
the Terraform Registry as [`smoketurner/discord`](https://registry.terraform.io/providers/smoketurner/discord/latest).

```terraform
terraform {
  required_providers {
    discord = {
      source = "smoketurner/discord"
    }
  }
}

provider "discord" {} # reads DISCORD_TOKEN

resource "discord_category_channel" "community" {
  server_id = var.server_id
  name      = "Community"
}

resource "discord_text_channel" "general" {
  server_id   = var.server_id
  name        = "general"
  category_id = discord_category_channel.community.id
}

resource "discord_role" "member" {
  server_id   = var.server_id
  name        = "Member"
  color       = provider::discord::color("#57F287")
  permissions = provider::discord::permissions(["VIEW_CHANNEL", "SEND_MESSAGES"])
}
```

See the [documentation](docs/index.md) for bot setup and every resource, data source and function.

## Resources, data sources and functions

| Kind | Names |
|------|-------|
| Server | `discord_server_settings`, `discord_server_incident_actions`, `discord_onboarding`, `discord_welcome_screen`, `discord_server_widget`, `data.discord_server`, `data.discord_server_preview`, `data.discord_server_vanity_url`, `data.discord_server_widget`, `data.discord_voice_regions`, `data.discord_audit_log` |
| Roles | `discord_role`, `discord_role_everyone`, `discord_role_positions`, `data.discord_role`, `data.discord_role_member_counts` |
| Channels | `discord_category_channel`, `discord_text_channel`, `discord_announcement_channel`, `discord_voice_channel`, `discord_stage_channel`, `discord_forum_channel`, `discord_media_channel`, `discord_channel_positions`, `discord_channel_follower`, `data.discord_channel` |
| Permissions | `discord_channel_permission` |
| Members | `discord_member`, `discord_member_role`, `discord_member_roles`, `discord_ban`, `data.discord_member` |
| Users and application | `data.discord_user`, `data.discord_current_user`, `data.discord_current_application` |
| Other | `discord_webhook`, `discord_invite`, `discord_message`, `discord_message_reaction`, `discord_thread`, `discord_emoji`, `data.discord_invite`, `data.discord_message`, `data.discord_pinned_messages`, `data.discord_sticker`, `data.discord_sticker_pack`, `data.discord_sticker_packs`, `data.discord_default_soundboard_sounds` |
| Events | `discord_scheduled_event`, `discord_stage_instance` |
| Monetization | `data.discord_skus`, `data.discord_entitlements`, `data.discord_sku_subscriptions` |
| Functions | `provider::discord::permissions`, `provider::discord::color` (Terraform 1.8+); `data.discord_permissions`, `data.discord_color` for older versions |

## Design notes

This provider is informed by the issues and pull requests of
[Lucky3028/terraform-provider-discord](https://github.com/Lucky3028/terraform-provider-discord):

- Resources deleted or changed in the Discord client are detected on refresh and recreated or corrected, instead of
  failing the plan.
- Updates send only the fields that changed, so unrelated settings are never reset.
- Role and channel ordering are separate resources that reorder everything in a single API request.
- A permission overwrite is written in one request, never removed and re-added.
- Imports populate every attribute, including `server_id`.
- Tokens work with or without the `Bot ` prefix.
- Forum channels support tags, default reactions, sort order and layout, and tag IDs are preserved when the tag list
  changes.
- Bots cannot create servers, so `discord_server_settings` adopts an existing server.
- Onboarding prompts and options are matched by title, so reordering them keeps their IDs. Destroying the onboarding,
  welcome screen or widget resource disables the feature and leaves its content in place.

## Development

Requires Go (see `go.mod`) and Terraform 1.8 or later.

```shell
make test      # unit and acceptance tests against an in-memory fake Discord API
make lint      # golangci-lint
make generate  # regenerate docs/ from schemas, examples/ and templates/
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for running the tests against a real Discord server and for how releases work.

## License

[MPL-2.0](LICENSE)
