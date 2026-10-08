# Changelog

## [0.2.0](https://github.com/smoketurner/terraform-provider-discord/compare/v0.1.0...v0.2.0) (2026-10-08)


### Features

* add audit log, invite, message, monetization and server info data sources ([#83](https://github.com/smoketurner/terraform-provider-discord/issues/83)) ([27421a6](https://github.com/smoketurner/terraform-provider-discord/commit/27421a6ecba79b9032672ec792097041e24914b8))
* add authoritative member roles, nickname and timeout ([#70](https://github.com/smoketurner/terraform-provider-discord/issues/70)) ([2b74317](https://github.com/smoketurner/terraform-provider-discord/commit/2b74317c051834d9c956516e368adc9e15119240))
* add bot profile, application settings, role connection metadata and application emoji ([0c79b6d](https://github.com/smoketurner/terraform-provider-discord/commit/0c79b6d6c3c739d9a642f6f07cc85884c751f6eb))
* add discord_application_command ([0c79b6d](https://github.com/smoketurner/terraform-provider-discord/commit/0c79b6d6c3c739d9a642f6f07cc85884c751f6eb))
* add discord_auto_moderation_rule ([0c79b6d](https://github.com/smoketurner/terraform-provider-discord/commit/0c79b6d6c3c739d9a642f6f07cc85884c751f6eb))
* add discord_ban ([#77](https://github.com/smoketurner/terraform-provider-discord/issues/77)) ([189b504](https://github.com/smoketurner/terraform-provider-discord/commit/189b504bc885c2753622d49f66b9bae87a9c7b56))
* add discord_media_channel ([#64](https://github.com/smoketurner/terraform-provider-discord/issues/64)) ([f8a0f75](https://github.com/smoketurner/terraform-provider-discord/commit/f8a0f753b1073e07e5ae4806763001efbf0a15ea))
* add discord_message_reaction and discord_channel_follower ([0c79b6d](https://github.com/smoketurner/terraform-provider-discord/commit/0c79b6d6c3c739d9a642f6f07cc85884c751f6eb))
* add discord_thread ([#69](https://github.com/smoketurner/terraform-provider-discord/issues/69)) ([7ff286d](https://github.com/smoketurner/terraform-provider-discord/commit/7ff286d73d07b308f28388eac20c3f800ceeaa8a))
* add list resources for terraform query ([1ea3511](https://github.com/smoketurner/terraform-provider-discord/commit/1ea3511b3e7f16bcda18e42d9e3fef8e9b5a8953))
* add multipart uploads and X-Audit-Log-Reason to the client ([#61](https://github.com/smoketurner/terraform-provider-discord/issues/61)) ([c38fc79](https://github.com/smoketurner/terraform-provider-discord/commit/c38fc792facdf7ae71994a0b39ed99cd56b152aa))
* add onboarding, welcome screen, and widget resources ([#74](https://github.com/smoketurner/terraform-provider-discord/issues/74)) ([a7d4877](https://github.com/smoketurner/terraform-provider-discord/commit/a7d4877c7379fc15fb5bfbc73b73e77a26de40f5)), closes [#18](https://github.com/smoketurner/terraform-provider-discord/issues/18) [#19](https://github.com/smoketurner/terraform-provider-discord/issues/19) [#28](https://github.com/smoketurner/terraform-provider-discord/issues/28)
* add plural data sources for listing server objects ([1ea3511](https://github.com/smoketurner/terraform-provider-discord/commit/1ea3511b3e7f16bcda18e42d9e3fef8e9b5a8953))
* add resource identity for import by identity ([#67](https://github.com/smoketurner/terraform-provider-discord/issues/67)) ([49870d3](https://github.com/smoketurner/terraform-provider-discord/commit/49870d39b3b523301dfcce9f42b094c5d44d475d))
* add scheduled event and stage instance resources ([#75](https://github.com/smoketurner/terraform-provider-discord/issues/75)) ([e5d45c5](https://github.com/smoketurner/terraform-provider-discord/commit/e5d45c51538ac906a812cb989e5591d366d63d83))
* add sticker and soundboard sound resources ([#72](https://github.com/smoketurner/terraform-provider-discord/issues/72)) ([23f00a5](https://github.com/smoketurner/terraform-provider-discord/commit/23f00a56516d43ccb71aafb50437b0586fb58f0a)), closes [#16](https://github.com/smoketurner/terraform-provider-discord/issues/16) [#26](https://github.com/smoketurner/terraform-provider-discord/issues/26)
* add Terraform actions, discord_server_template and invite role grants ([0c79b6d](https://github.com/smoketurner/terraform-provider-discord/commit/0c79b6d6c3c739d9a642f6f07cc85884c751f6eb))
* add user, application, permissions and color data sources ([#80](https://github.com/smoketurner/terraform-provider-discord/issues/80)) ([908ba30](https://github.com/smoketurner/terraform-provider-discord/commit/908ba30a82062bbac66a3d3e25c0dab5e408cf59))
* add write-only image attributes ([#65](https://github.com/smoketurner/terraform-provider-discord/issues/65)) ([7451091](https://github.com/smoketurner/terraform-provider-discord/commit/7451091acfda7a2f265bdad5f13bce51d620b649))
* cover remaining server settings and incident actions ([#73](https://github.com/smoketurner/terraform-provider-discord/issues/73)) ([915c54a](https://github.com/smoketurner/terraform-provider-discord/commit/915c54a6baf07dc2ed31c0b48a845fc91534ff49))
* ephemeral webhook secrets and discord_webhook_message ([#78](https://github.com/smoketurner/terraform-provider-discord/issues/78)) ([277067e](https://github.com/smoketurner/terraform-provider-discord/commit/277067e098f6d18f2d29c36201f0c9d3e8aeda8f))
* initial permission overwrites and in-place text/announcement conversion ([#68](https://github.com/smoketurner/terraform-provider-discord/issues/68)) ([8e41b5d](https://github.com/smoketurner/terraform-provider-discord/commit/8e41b5d8efd39970a221f61cc38ace009a52e517))
* support embed suppression, attachments, components, polls and stickers on discord_message ([1ea3511](https://github.com/smoketurner/terraform-provider-discord/commit/1ea3511b3e7f16bcda18e42d9e3fef8e9b5a8953))
* support role icon images on discord_role ([#76](https://github.com/smoketurner/terraform-provider-discord/issues/76)) ([ea769d5](https://github.com/smoketurner/terraform-provider-discord/commit/ea769d56cb449999a8c8de2dbc94266456f7fe6a))
* track rate limits by X-RateLimit-Bucket ([#66](https://github.com/smoketurner/terraform-provider-discord/issues/66)) ([9d791c7](https://github.com/smoketurner/terraform-provider-discord/commit/9d791c700dedb45831db7fc0f7c880399b2b7166))


### Bug Fixes

* correct behavior found by live acceptance tests ([#90](https://github.com/smoketurner/terraform-provider-discord/issues/90)) ([81a1fee](https://github.com/smoketurner/terraform-provider-discord/commit/81a1fee18828837fdfea52c7708cb271a69eff71))
* handle channels hidden from the bot in channel positions ([#59](https://github.com/smoketurner/terraform-provider-discord/issues/59)) ([d2d3c33](https://github.com/smoketurner/terraform-provider-discord/commit/d2d3c330995427a8b4868269a2e3b2d5758bdffd))
* preserve unmanaged channel flags and unlisted role positions ([#60](https://github.com/smoketurner/terraform-provider-discord/issues/60)) ([3c57262](https://github.com/smoketurner/terraform-provider-discord/commit/3c572623050edfb73c2a7b2858e93473f1bb2629))

## 0.1.0 (2026-10-08)


### Features

* Discord provider on the Terraform Plugin Framework ([#2](https://github.com/smoketurner/terraform-provider-discord/issues/2)) ([b0206c8](https://github.com/smoketurner/terraform-provider-discord/commit/b0206c8eed97a60c886b69d2cb9ba9ba9546f87b))
