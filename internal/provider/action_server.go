package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ action.ActionWithConfigure = &pruneMembersAction{}
	_ action.ActionWithConfigure = &bulkBanAction{}
	_ action.ActionWithConfigure = &syncServerTemplateAction{}
	_ action.ActionWithConfigure = &setVoiceChannelStatusAction{}
)

type pruneMembersAction struct{ actionClient }

type pruneMembersModel struct {
	ServerID          types.String `tfsdk:"server_id"`
	Days              types.Int64  `tfsdk:"days"`
	IncludeRoleIDs    types.Set    `tfsdk:"include_role_ids"`
	ComputePruneCount types.Bool   `tfsdk:"compute_prune_count"`
	DryRun            types.Bool   `tfsdk:"dry_run"`
	AuditLogReason    types.String `tfsdk:"audit_log_reason"`
}

func newPruneMembersAction() action.Action { return &pruneMembersAction{} }

func (a *pruneMembersAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_prune_members"
}

func (a *pruneMembersAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Removes members who have not been active for a number of days. By default only " +
			"members without roles are removed. With `dry_run`, the action reports how many members would be " +
			"removed and removes nobody. Requires the Manage Server and Kick Members permissions, or Administrator " +
			"when the server has the `PRUNE_REQUIRES_ADMIN` feature.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": actionAuditLogReason(),
			"server_id":        actionID("ID of the server (guild)."),
			"days": schema.Int64Attribute{
				MarkdownDescription: "Days of inactivity, from 1 to 30. Discord defaults to 7.",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.Between(1, 30)},
			},
			"include_role_ids": schema.SetAttribute{
				MarkdownDescription: "Roles whose members can also be removed. A member is removed only when every " +
					"role they have is in this set.",
				ElementType: types.StringType,
				Optional:    true,
				Validators:  []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(snowflakeValidator())},
			},
			"compute_prune_count": schema.BoolAttribute{
				MarkdownDescription: "Whether Discord counts the removed members. Discord recommends `false` for large " +
					"servers. Defaults to `true`. Ignored with `dry_run`.",
				Optional: true,
			},
			"dry_run": schema.BoolAttribute{
				MarkdownDescription: "Report how many members would be removed without removing any. Defaults to `false`.",
				Optional:            true,
			},
		},
	}
}

func (a *pruneMembersAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var cfg pruneMembersModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var roles []string
	if !cfg.IncludeRoleIDs.IsNull() {
		resp.Diagnostics.Append(cfg.IncludeRoleIDs.ElementsAs(ctx, &roles, false)...)
	}
	days := cfg.Days.ValueInt64()
	if cfg.Days.IsNull() {
		days = 7
	}
	serverID := cfg.ServerID.ValueString()
	if cfg.DryRun.ValueBool() {
		r, err := a.client.PreviewPrune(ctx, serverID, days, roles)
		if err != nil {
			apiError(&resp.Diagnostics, "count members to prune", err)
			return
		}
		progress(resp, "Dry run: %d members would be pruned", pruneCount(r))
		return
	}
	body := discord.Payload{"days": days}
	if roles != nil {
		body["include_roles"] = roles
	}
	putBool(body, "compute_prune_count", cfg.ComputePruneCount)
	r, err := a.client.PruneMembers(withAuditLogReason(ctx, cfg.AuditLogReason), serverID, body)
	if err != nil {
		apiError(&resp.Diagnostics, "prune members", err)
		return
	}
	if r.Pruned == nil {
		progress(resp, "Started pruning members")
		return
	}
	progress(resp, "Pruned %d members", *r.Pruned)
}

func pruneCount(r *discord.PruneResult) int64 {
	if r.Pruned == nil {
		return 0
	}
	return *r.Pruned
}

type bulkBanAction struct{ actionClient }

type bulkBanModel struct {
	ServerID             types.String `tfsdk:"server_id"`
	UserIDs              types.Set    `tfsdk:"user_ids"`
	DeleteMessageSeconds types.Int64  `tfsdk:"delete_message_seconds"`
	AuditLogReason       types.String `tfsdk:"audit_log_reason"`
}

func newBulkBanAction() action.Action { return &bulkBanAction{} }

func (a *bulkBanAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bulk_ban"
}

func (a *bulkBanAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Bans up to 200 users from a server in one request, for example after a raid. Users " +
			"Discord could not ban, or who were already banned, are reported in a warning; the action fails only " +
			"when nobody could be banned. Use `discord_ban` for bans Terraform keeps in place. Requires the Ban " +
			"Members and Manage Server permissions.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": schema.StringAttribute{
				MarkdownDescription: "Reason recorded in the server's audit log and on each ban, overriding the " +
					"provider's `audit_log_reason`. Up to 512 characters.",
				Optional:   true,
				Validators: []validator.String{auditLogReasonValidator()},
			},
			"server_id": actionID("ID of the server (guild)."),
			"user_ids":  actionIDSet("IDs of the users to ban, at most 200.", 1, 200),
			"delete_message_seconds": schema.Int64Attribute{
				MarkdownDescription: "Number of seconds of each user's message history to delete, from 0 to 604800 " +
					"(7 days). Defaults to 0.",
				Optional:   true,
				Validators: []validator.Int64{int64validator.Between(0, maxBanDeleteMessageSeconds)},
			},
		},
	}
}

func (a *bulkBanAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var cfg bulkBanModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var ids []string
	resp.Diagnostics.Append(cfg.UserIDs.ElementsAs(ctx, &ids, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := discord.Payload{"user_ids": ids}
	putKnownInt(body, "delete_message_seconds", cfg.DeleteMessageSeconds)
	r, err := a.client.BulkBan(withAuditLogReason(ctx, cfg.AuditLogReason), cfg.ServerID.ValueString(), body)
	if err != nil {
		apiError(&resp.Diagnostics, "ban users", err)
		return
	}
	progress(resp, "Banned %d users", len(r.BannedUsers))
	if len(r.FailedUsers) > 0 {
		resp.Diagnostics.AddWarning("Some users were not banned",
			"Discord could not ban these users, or they were already banned: "+strings.Join(r.FailedUsers, ", ")+".")
	}
}

type syncServerTemplateAction struct{ actionClient }

type syncServerTemplateModel struct {
	ServerID types.String `tfsdk:"server_id"`
	Code     types.String `tfsdk:"code"`
}

func newSyncServerTemplateAction() action.Action { return &syncServerTemplateAction{} }

func (a *syncServerTemplateAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sync_server_template"
}

func (a *syncServerTemplateAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Updates a server template's snapshot to the server's current settings, roles and " +
			"channels, for example after Terraform changes them. Requires the Manage Server permission.",
		Attributes: map[string]schema.Attribute{
			"server_id": actionID("ID of the server (guild) the template belongs to."),
			"code": schema.StringAttribute{
				MarkdownDescription: "Template code, such as `discord_server_template.example.code`.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
		},
	}
}

func (a *syncServerTemplateAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var cfg syncServerTemplateModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := a.client.SyncGuildTemplate(ctx, cfg.ServerID.ValueString(), cfg.Code.ValueString()); err != nil {
		apiError(&resp.Diagnostics, "sync server template", err)
		return
	}
	progress(resp, "Synced server template %s", cfg.Code.ValueString())
}

type setVoiceChannelStatusAction struct{ actionClient }

type setVoiceChannelStatusModel struct {
	ChannelID      types.String `tfsdk:"channel_id"`
	Status         types.String `tfsdk:"status"`
	AuditLogReason types.String `tfsdk:"audit_log_reason"`
}

func newSetVoiceChannelStatusAction() action.Action { return &setVoiceChannelStatusAction{} }

func (a *setVoiceChannelStatusAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_set_voice_channel_status"
}

func (a *setVoiceChannelStatusAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Sets the status line shown on a voice channel, or clears it. The status is not part of " +
			"the channel and Discord's REST API cannot read it back, so it is set by an action rather than managed " +
			"as an attribute of `discord_voice_channel`. Requires the Set Voice Channel Status permission, and also " +
			"Manage Channels when the bot is not connected to the channel.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": actionAuditLogReason(),
			"channel_id":       actionID("ID of the voice channel."),
			"status": schema.StringAttribute{
				MarkdownDescription: "Status text, up to 500 characters. Omit it to clear the status.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 500)},
			},
		},
	}
}

func (a *setVoiceChannelStatusAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var cfg setVoiceChannelStatusModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, cfg.AuditLogReason)
	if err := a.client.SetVoiceChannelStatus(ctx, cfg.ChannelID.ValueString(), cfg.Status.ValueStringPointer()); err != nil {
		apiError(&resp.Diagnostics, "set voice channel status", err)
		return
	}
	if cfg.Status.IsNull() {
		progress(resp, "Cleared the status of channel %s", cfg.ChannelID.ValueString())
		return
	}
	progress(resp, "Set the status of channel %s", cfg.ChannelID.ValueString())
}
