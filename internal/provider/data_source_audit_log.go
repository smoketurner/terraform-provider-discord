package provider

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// maxAuditLogPage is the most entries Discord returns per request.
const maxAuditLogPage = 100

type auditLogDataSource struct{ readOnlyDataSource }

type auditLogChangeModel struct {
	Key      types.String `tfsdk:"key"`
	OldValue types.String `tfsdk:"old_value"`
	NewValue types.String `tfsdk:"new_value"`
}

type auditLogEntryModel struct {
	ID         types.String          `tfsdk:"id"`
	ActionType types.Int64           `tfsdk:"action_type"`
	UserID     types.String          `tfsdk:"user_id"`
	TargetID   types.String          `tfsdk:"target_id"`
	Reason     types.String          `tfsdk:"reason"`
	Options    types.Map             `tfsdk:"options"`
	Changes    []auditLogChangeModel `tfsdk:"changes"`
}

type auditLogDataModel struct {
	ServerID   types.String         `tfsdk:"server_id"`
	UserID     types.String         `tfsdk:"user_id"`
	ActionType types.Int64          `tfsdk:"action_type"`
	Before     types.String         `tfsdk:"before"`
	After      types.String         `tfsdk:"after"`
	Limit      types.Int64          `tfsdk:"limit"`
	Entries    []auditLogEntryModel `tfsdk:"entries"`
}

func newAuditLogDataSource() datasource.DataSource { return &auditLogDataSource{} }

func (d *auditLogDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_audit_log"
}

func (d *auditLogDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	optionalSnowflake := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc, Optional: true, Validators: []validator.String{snowflakeValidator()}}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads entries from a server's audit log, for example to check who changed a setting " +
			"outside Terraform. The bot needs the View Audit Log permission. Discord keeps entries for 45 days.\n\n" +
			"Entries are newest first, or oldest first when `after` is set. Change values vary by action and field, " +
			"so they are JSON-encoded; decode them with `jsondecode`.",
		Attributes: map[string]schema.Attribute{
			"server_id": dsServerID(),
			"user_id":   optionalSnowflake("Only return entries for actions by this user or app."),
			"action_type": schema.Int64Attribute{
				MarkdownDescription: "Only return entries of this [audit log event](https://docs.discord.com/developers/resources/audit-log#audit-log-entry-object-audit-log-events) " +
					"type, such as `22` for bans.",
				Optional:   true,
				Validators: []validator.Int64{int64validator.AtLeast(1)},
			},
			"before": optionalSnowflake("Only return entries with an ID lower than this entry ID."),
			"after":  optionalSnowflake("Only return entries with an ID higher than this entry ID. Entries are then returned oldest first."),
			"limit": schema.Int64Attribute{
				MarkdownDescription: "Maximum number of entries to return. Defaults to 50. Discord returns at most 100 per " +
					"request, so higher limits take several requests.",
				Optional:   true,
				Validators: []validator.Int64{int64validator.AtLeast(1)},
			},
			"entries": computedList("Audit log entries.", map[string]schema.Attribute{
				"id":          computedString("Entry ID."),
				"action_type": computedInt("Audit log event type."),
				"user_id":     computedString("ID of the user or app that made the change."),
				"target_id":   computedString("ID of the affected object, such as a role, channel or user."),
				"reason":      computedString("Reason given for the change."),
				"options": schema.MapAttribute{
					MarkdownDescription: "Additional information for some event types, such as `channel_id` or `count`.",
					ElementType:         types.StringType,
					Computed:            true,
				},
				"changes": computedList("Changes made to the target.", map[string]schema.Attribute{
					"key":       computedString("Name of the changed field."),
					"old_value": computedString("JSON-encoded value before the change, or null if it was null."),
					"new_value": computedString("JSON-encoded value after the change, or null if it was reset."),
				}),
			}),
		},
	}
}

func (d *auditLogDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m auditLogDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	limit := int64(50)
	if !m.Limit.IsNull() {
		limit = m.Limit.ValueInt64()
	}
	q := discord.AuditLogQuery{
		UserID:     m.UserID.ValueString(),
		ActionType: m.ActionType.ValueInt64(),
		Before:     m.Before.ValueString(),
		After:      m.After.ValueString(),
	}
	ascending := q.After != ""
	m.Entries = []auditLogEntryModel{}
	for remaining := limit; remaining > 0; {
		q.Limit = int(min(remaining, maxAuditLogPage))
		page, err := d.client.GetGuildAuditLog(ctx, m.ServerID.ValueString(), q)
		if err != nil {
			apiError(&resp.Diagnostics, "read audit log", err)
			return
		}
		for _, e := range page.AuditLogEntries {
			m.Entries = append(m.Entries, auditLogEntryValue(ctx, e, resp))
		}
		n := len(page.AuditLogEntries)
		if n < q.Limit {
			break
		}
		remaining -= int64(n)
		if last := page.AuditLogEntries[n-1].ID; ascending {
			q.After = last
		} else {
			q.Before = last
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func auditLogEntryValue(ctx context.Context, e discord.AuditLogEntry, resp *datasource.ReadResponse) auditLogEntryModel {
	options := map[string]string{}
	for k, raw := range e.Options {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			s = string(raw)
		}
		options[k] = s
	}
	opts, diags := types.MapValueFrom(ctx, types.StringType, options)
	resp.Diagnostics.Append(diags...)
	entry := auditLogEntryModel{
		ID:         types.StringValue(e.ID),
		ActionType: types.Int64Value(e.ActionType),
		UserID:     stringPtrValue(e.UserID),
		TargetID:   stringPtrValue(e.TargetID),
		Reason:     stringPtrValue(e.Reason),
		Options:    opts,
		Changes:    []auditLogChangeModel{},
	}
	for _, c := range e.Changes {
		entry.Changes = append(entry.Changes, auditLogChangeModel{
			Key:      types.StringValue(c.Key),
			OldValue: rawJSONValue(c.OldValue),
			NewValue: rawJSONValue(c.NewValue),
		})
	}
	return entry
}

// rawJSONValue returns a JSON value as a string, or null when it is absent
// or null.
func rawJSONValue(raw json.RawMessage) types.String {
	if len(raw) == 0 || string(raw) == "null" {
		return types.StringNull()
	}
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return types.StringValue(string(raw))
	}
	return types.StringValue(b.String())
}
