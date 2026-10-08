package provider

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// lucky3028Address is the registry address of Lucky3028/discord, an archived
// provider whose resources can be moved to this provider with moved blocks.
const lucky3028Address = "registry.terraform.io/lucky3028/discord"

// luckyTranslator returns the attributes of the target resource that can be
// derived from a Lucky3028/discord resource's state.
type luckyTranslator func(ctx context.Context, s luckyState, diags *diag.Diagnostics) map[string]any

// luckyMover moves the state of the Lucky3028/discord resource type source.
// Attributes the translator does not return are left null, and the refresh
// that follows the move fills them in from Discord, as after an import.
func luckyMover(ri resourceIdentity, source string, translate luckyTranslator) resource.StateMover {
	return resource.StateMover{
		StateMover: func(ctx context.Context, req resource.MoveStateRequest, resp *resource.MoveStateResponse) {
			if !strings.EqualFold(req.SourceProviderAddress, lucky3028Address) || req.SourceTypeName != source {
				return
			}
			summary := "Unable to move " + source + " state"
			s, err := decodeLuckyState(req.SourceRawState.JSON)
			if err != nil {
				resp.Diagnostics.AddError(summary, "The Lucky3028/discord state could not be read: "+err.Error())
				return
			}
			attrs := translate(ctx, s, &resp.Diagnostics)
			if resp.Diagnostics.HasError() {
				return
			}
			raw, err := nullAttributes(resp.TargetState.Schema.Type().TerraformType(ctx))
			if err != nil {
				resp.Diagnostics.AddError(summary, err.Error())
				return
			}
			resp.TargetState.Raw = raw
			for name, v := range attrs {
				resp.Diagnostics.Append(resp.TargetState.SetAttribute(ctx, path.Root(name), v)...)
			}
			if resp.TargetIdentity != nil {
				ri.setIdentity(ctx, resp.TargetIdentity, &resp.Diagnostics, &resp.TargetState)
			}
		},
	}
}

// nullAttributes returns an object of the given type whose attributes are all
// null.
func nullAttributes(t tftypes.Type) (tftypes.Value, error) {
	return copySharedAttributes(tftypes.NewValue(tftypes.Object{}, map[string]tftypes.Value{}), t)
}

func decodeLuckyState(raw []byte) (luckyState, error) {
	if raw == nil {
		return nil, errors.New("the state is in a format from Terraform 0.11 or earlier")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var s luckyState
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	return s, nil
}

// luckyState is the decoded state of a Lucky3028/discord resource. That
// provider is built on the SDKv2, which records an unset string as "" and an
// unset list as [], so both are read as null.
type luckyState map[string]any

// require reports an error for each named attribute that is missing or empty.
// They identify the Discord object, so the move cannot continue without them.
func (s luckyState) require(diags *diag.Diagnostics, names ...string) {
	for _, name := range names {
		if s.str(name).IsNull() {
			diags.AddError("Unable to move Lucky3028/discord state",
				fmt.Sprintf("The state has no %q attribute, which identifies the Discord object. Refresh the resource "+
					"with Lucky3028/discord, or import it into this provider instead.", name))
		}
	}
}

func (s luckyState) str(name string) types.String {
	if v, ok := s[name].(string); ok && v != "" {
		return types.StringValue(v)
	}
	return types.StringNull()
}

func (s luckyState) int(name string) types.Int64 {
	n, ok := s[name].(json.Number)
	if !ok {
		return types.Int64Null()
	}
	v, err := n.Int64()
	if err != nil {
		return types.Int64Null()
	}
	return types.Int64Value(v)
}

func (s luckyState) bool(name string) types.Bool {
	if v, ok := s[name].(bool); ok {
		return types.BoolValue(v)
	}
	return types.BoolNull()
}

// bitfield converts an integer bitfield, such as permissions, to the decimal
// string this provider uses.
func (s luckyState) bitfield(name string) types.String {
	n := s.int(name)
	if n.IsNull() {
		return types.StringNull()
	}
	return types.StringValue(strconv.FormatInt(n.ValueInt64(), 10))
}

// enum converts an integer to the name this provider uses for it.
func (s luckyState) enum(name string, m enumMapping) types.String {
	n := s.int(name)
	if n.IsNull() {
		return types.StringNull()
	}
	return m.name(n.ValueInt64())
}

// blocks returns the nested blocks of a list or set block.
func (s luckyState) blocks(name string) []luckyState {
	items, _ := s[name].([]any)
	out := make([]luckyState, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func (s luckyState) strings(name string) []string {
	items, _ := s[name].([]any)
	var out []string
	for _, item := range items {
		if v, ok := item.(string); ok && v != "" {
			out = append(out, v)
		}
	}
	return out
}

// stringSet converts a list or set of strings, null when empty.
func (s luckyState) stringSet(ctx context.Context, name string, diags *diag.Diagnostics) types.Set {
	return optionalStringSet(ctx, s.strings(name), diags)
}

// luckyChannel translates the state of a Lucky3028/discord channel of this
// kind.
func (r *channelResource[T, PT]) luckyChannel(_ context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
	s.require(diags, "id", "server_id")
	attrs := map[string]any{
		"id":        s.str("id"),
		"server_id": s.str("server_id"),
		"name":      s.str("name"),
		"position":  s.int("position"),
	}
	// Lucky3028/discord has no settings beyond these, and its
	// sync_perms_with_category has no equivalent.
	shared := map[string]any{
		"category_id":         s.str("category"),
		"topic":               s.str("topic"),
		"nsfw":                s.bool("nsfw"),
		"rate_limit_per_user": s.int("rate_limit_per_user"),
		"bitrate":             s.int("bitrate"),
		"user_limit":          s.int("user_limit"),
	}
	for name, v := range shared {
		if _, ok := r.kind.attributes[name]; ok {
			attrs[name] = v
		}
	}
	return attrs
}

// MoveState moves discord_channel_permission from Lucky3028/discord, whose
// type "user" is "member" here and whose bitfields are integers.
func (r *channelPermissionResource) MoveState(context.Context) []resource.StateMover {
	return []resource.StateMover{luckyMover(r.resourceIdentity, "discord_channel_permission",
		func(_ context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, "channel_id", "overwrite_id")
			kind := s.str("type")
			if kind.ValueString() == "user" {
				kind = types.StringValue("member")
			}
			return map[string]any{
				"id":           types.StringValue(s.str("channel_id").ValueString() + "/" + s.str("overwrite_id").ValueString()),
				"channel_id":   s.str("channel_id"),
				"overwrite_id": s.str("overwrite_id"),
				"type":         kind,
				"allow":        s.bitfield("allow"),
				"deny":         s.bitfield("deny"),
			}
		})}
}

// MoveState moves discord_role from Lucky3028/discord.
func (r *roleResource) MoveState(context.Context) []resource.StateMover {
	return []resource.StateMover{luckyMover(r.resourceIdentity, "discord_role",
		func(_ context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, "id", "server_id")
			return map[string]any{
				"id":          s.str("id"),
				"server_id":   s.str("server_id"),
				"name":        s.str("name"),
				"permissions": s.bitfield("permissions"),
				"color":       s.int("color"),
				"hoist":       s.bool("hoist"),
				"mentionable": s.bool("mentionable"),
				"position":    s.int("position"),
				"managed":     s.bool("managed"),
			}
		})}
}

// MoveState moves discord_role_everyone from Lucky3028/discord.
func (r *roleEveryoneResource) MoveState(context.Context) []resource.StateMover {
	return []resource.StateMover{luckyMover(r.resourceIdentity, "discord_role_everyone",
		func(_ context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, "server_id")
			return map[string]any{
				"id":          s.str("server_id"),
				"server_id":   s.str("server_id"),
				"permissions": s.bitfield("permissions"),
			}
		})}
}

// MoveState moves discord_role_positions from Lucky3028/discord, whose
// position blocks become role_ids ordered from the highest position to the
// lowest. Channel positions have no Lucky3028/discord equivalent.
func (r *positionsResource) MoveState(context.Context) []resource.StateMover {
	if r.kind.idsAttr != "role_ids" {
		return nil
	}
	return []resource.StateMover{luckyMover(resourceIdentity{}, "discord_role_positions",
		func(ctx context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, "server_id")
			blocks := s.blocks("position")
			slices.SortStableFunc(blocks, func(a, b luckyState) int {
				return cmp.Compare(b.int("position").ValueInt64(), a.int("position").ValueInt64())
			})
			ids := make([]string, 0, len(blocks))
			for _, b := range blocks {
				ids = append(ids, b.str("role_id").ValueString())
			}
			return map[string]any{
				"id":        s.str("server_id"),
				"server_id": s.str("server_id"),
				"role_ids":  stringListValue(ctx, ids, diags),
			}
		})}
}

// MoveState moves discord_member_roles from Lucky3028/discord. That resource
// only adds and removes the roles it lists, while this one sets the member's
// complete list of roles, so role_ids starts as the roles listed with
// has_role = true. The refresh after the move replaces it with every role the
// member has, and the plan then shows the roles the configuration would
// revoke.
func (r *memberRolesResource) MoveState(context.Context) []resource.StateMover {
	return []resource.StateMover{luckyMover(r.resourceIdentity, "discord_member_roles",
		func(ctx context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, "server_id", "user_id")
			var ids []string
			for _, b := range s.blocks("role") {
				if b.bool("has_role").ValueBool() && !b.str("role_id").IsNull() {
					ids = append(ids, b.str("role_id").ValueString())
				}
			}
			return map[string]any{
				"id":        types.StringValue(s.str("server_id").ValueString() + "/" + s.str("user_id").ValueString()),
				"server_id": s.str("server_id"),
				"user_id":   s.str("user_id"),
				"role_ids":  stringSetValue(ctx, ids, diags),
			}
		})}
}

// MoveState moves discord_message from Lucky3028/discord. Its file blocks
// become attachments, keeping the local source path, which Discord does not
// return. The embed is read back from Discord by the refresh.
func (r *messageResource) MoveState(context.Context) []resource.StateMover {
	return []resource.StateMover{luckyMover(r.resourceIdentity, "discord_message",
		func(_ context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, "id", "channel_id")
			attrs := map[string]any{
				"id":         s.str("id"),
				"channel_id": s.str("channel_id"),
				"content":    s.str("content"),
				"pinned":     s.bool("pinned"),
				"author_id":  s.str("author"),
			}
			files := s.blocks("file")
			if len(files) == 0 {
				return attrs
			}
			atts := make([]attachmentModel, 0, len(files))
			for _, f := range files {
				atts = append(atts, attachmentModel{
					Filename:      f.str("filename"),
					Source:        f.str("source"),
					ContentBase64: types.StringNull(),
					SourceHash:    types.StringNull(),
					Description:   types.StringNull(),
					Spoiler:       types.BoolNull(),
					ID:            f.str("id"),
					Size:          f.int("size"),
					ContentType:   f.str("content_type"),
				})
			}
			attrs["attachments"] = atts
			return attrs
		})}
}

// MoveState moves discord_webhook from Lucky3028/discord, which stored the
// webhook token in state, so store_secrets starts as true. An avatar set with
// avatar_data_uri becomes avatar; one downloaded from avatar_url cannot be
// recovered.
func (r *webhookResource) MoveState(context.Context) []resource.StateMover {
	return []resource.StateMover{luckyMover(r.resourceIdentity, "discord_webhook",
		func(_ context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, "id")
			return map[string]any{
				"id":            s.str("id"),
				"channel_id":    s.str("channel_id"),
				"name":          s.str("name"),
				"avatar":        s.str("avatar_data_uri"),
				"avatar_hash":   s.str("avatar_hash"),
				"store_secrets": types.BoolValue(true),
				"token":         s.str("token"),
				"url":           s.str("url"),
			}
		})}
}

// MoveState moves discord_invite from Lucky3028/discord. Discord does not
// return unique, so the value Lucky3028/discord created the invite with,
// false unless configured, is kept.
func (r *inviteResource) MoveState(context.Context) []resource.StateMover {
	return []resource.StateMover{luckyMover(r.resourceIdentity, "discord_invite",
		func(_ context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, "id", "channel_id")
			return map[string]any{
				"id":         s.str("id"),
				"code":       s.str("id"),
				"channel_id": s.str("channel_id"),
				"max_age":    s.int("max_age"),
				"max_uses":   s.int("max_uses"),
				"temporary":  s.bool("temporary"),
				"unique":     s.bool("unique"),
			}
		})}
}

// MoveState moves discord_guild_sticker from Lucky3028/discord. Its file is
// a path, while file here is the file's content, so file starts null and
// configuring it only updates state.
func (r *stickerResource) MoveState(context.Context) []resource.StateMover {
	return []resource.StateMover{luckyMover(r.resourceIdentity, "discord_guild_sticker",
		func(_ context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, "id", "server_id")
			return map[string]any{
				"id":          s.str("id"),
				"server_id":   s.str("server_id"),
				"name":        s.str("name"),
				"description": s.str("description"),
				"tags":        s.str("tags"),
				"format_type": s.enum("format_type", stickerFormatTypes),
			}
		})}
}

// MoveState moves the Lucky3028/discord resources that manage an existing
// server's settings. This provider cannot create servers, so moving
// discord_server stops Terraform from deleting the server on destroy.
func (r *serverSettingsResource) MoveState(context.Context) []resource.StateMover {
	server := func(idAttr string) luckyTranslator {
		return func(_ context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, idAttr)
			return map[string]any{
				"id":                            s.str(idAttr),
				"server_id":                     s.str(idAttr),
				"name":                          s.str("name"),
				"description":                   s.str("description"),
				"verification_level":            s.enum("verification_level", verificationLevels),
				"default_message_notifications": s.enum("default_message_notifications", messageNotifications),
				"explicit_content_filter":       s.enum("explicit_content_filter", explicitContentFilter),
				"afk_channel_id":                s.str("afk_channel_id"),
				"afk_timeout":                   s.int("afk_timeout"),
				"icon":                          s.str("icon_data_uri"),
				"icon_hash":                     s.str("icon_hash"),
				"splash":                        s.str("splash_data_uri"),
				"splash_hash":                   s.str("splash_hash"),
				"owner_id":                      s.str("owner_id"),
			}
		}
	}
	return []resource.StateMover{
		luckyMover(r.resourceIdentity, "discord_server", server("id")),
		luckyMover(r.resourceIdentity, "discord_managed_server", server("server_id")),
		luckyMover(r.resourceIdentity, "discord_system_channel",
			func(_ context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
				s.require(diags, "server_id")
				return map[string]any{
					"id":                   s.str("server_id"),
					"server_id":            s.str("server_id"),
					"system_channel_id":    s.str("system_channel_id"),
					"system_channel_flags": s.int("system_channel_flags"),
				}
			}),
	}
}

// MoveState moves discord_server_onboarding from Lucky3028/discord. Its state
// records the prompts as Discord returns them, not as configured, so prompts
// are read back by the refresh as after an import.
func (r *onboardingResource) MoveState(context.Context) []resource.StateMover {
	return []resource.StateMover{luckyMover(r.resourceIdentity, "discord_server_onboarding",
		func(ctx context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, "server_id")
			return map[string]any{
				"id":                  s.str("server_id"),
				"server_id":           s.str("server_id"),
				"enabled":             s.bool("enabled"),
				"mode":                s.enum("mode", onboardingModes),
				"default_channel_ids": s.stringSet(ctx, "default_channel_ids", diags),
			}
		})}
}

// MoveState moves discord_server_widget from Lucky3028/discord.
func (r *serverWidgetResource) MoveState(context.Context) []resource.StateMover {
	return []resource.StateMover{luckyMover(r.resourceIdentity, "discord_server_widget",
		func(_ context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, "server_id")
			return map[string]any{
				"id":         s.str("server_id"),
				"server_id":  s.str("server_id"),
				"enabled":    s.bool("enabled"),
				"channel_id": s.str("channel_id"),
			}
		})}
}

// MoveState moves discord_auto_moderation_rule from Lucky3028/discord. The
// trigger metadata and actions are read back by the refresh.
func (r *autoModerationRuleResource) MoveState(context.Context) []resource.StateMover {
	return []resource.StateMover{luckyMover(r.resourceIdentity, "discord_auto_moderation_rule",
		func(ctx context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, "id", "server_id")
			return map[string]any{
				"id":                 s.str("id"),
				"server_id":          s.str("server_id"),
				"name":               s.str("name"),
				"event_type":         s.enum("event_type", automodEventTypes),
				"trigger_type":       s.enum("trigger_type", automodTriggerTypes),
				"enabled":            s.bool("enabled"),
				"exempt_role_ids":    s.stringSet(ctx, "exempt_roles", diags),
				"exempt_channel_ids": s.stringSet(ctx, "exempt_channels", diags),
				"creator_id":         s.str("creator_id"),
			}
		})}
}

// MoveState moves discord_role_connection_metadata from Lucky3028/discord.
func (r *roleConnectionMetadataResource) MoveState(context.Context) []resource.StateMover {
	return []resource.StateMover{luckyMover(r.resourceIdentity, "discord_role_connection_metadata",
		func(_ context.Context, s luckyState, diags *diag.Diagnostics) map[string]any {
			s.require(diags, "application_id")
			records := make([]roleConnectionMetadataRecord, 0)
			for _, b := range s.blocks("metadata") {
				records = append(records, roleConnectionMetadataRecord{
					Type:                     b.enum("type", roleConnectionMetadataTypes),
					Key:                      b.str("key"),
					Name:                     b.str("name"),
					NameLocalizations:        types.MapNull(types.StringType),
					Description:              b.str("description"),
					DescriptionLocalizations: types.MapNull(types.StringType),
				})
			}
			return map[string]any{
				"id":             s.str("application_id"),
				"application_id": s.str("application_id"),
				"records":        records,
			}
		})}
}
