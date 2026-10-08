package provider

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ action.ActionWithConfigure = &sendMessageAction{}
	_ action.ActionWithConfigure = &crosspostMessageAction{}
	_ action.ActionWithConfigure = &endPollAction{}
	_ action.ActionWithConfigure = &bulkDeleteMessagesAction{}
)

type sendMessageAction struct{ actionClient }

type sendMessageModel struct {
	ChannelID       types.String `tfsdk:"channel_id"`
	Content         types.String `tfsdk:"content"`
	AllowedMentions types.Set    `tfsdk:"allowed_mentions"`
}

func newSendMessageAction() action.Action { return &sendMessageAction{} }

func (a *sendMessageAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_send_message"
}

func (a *sendMessageAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Posts a message to a channel each time the action runs, for example to announce a " +
			"deployment when another resource changes. Use `discord_message` instead for a message Terraform keeps " +
			"up to date. Requires the Send Messages permission.",
		Attributes: map[string]schema.Attribute{
			"channel_id": actionID("ID of the channel to post in."),
			"content": schema.StringAttribute{
				MarkdownDescription: "Message text (up to 2000 characters).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 2000)},
			},
			"allowed_mentions": schema.SetAttribute{
				MarkdownDescription: "Mention types that notify people: any of `roles`, `users` and `everyone`. " +
					"Defaults to none, so the message never pings anyone.",
				ElementType: types.StringType,
				Optional:    true,
				Validators:  []validator.Set{setvalidator.ValueStringsAre(stringvalidator.OneOf("roles", "users", "everyone"))},
			},
		},
	}
}

func (a *sendMessageAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var cfg sendMessageModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	parse := []string{}
	if !cfg.AllowedMentions.IsNull() {
		resp.Diagnostics.Append(cfg.AllowedMentions.ElementsAs(ctx, &parse, false)...)
	}
	msg, err := a.client.CreateMessage(ctx, cfg.ChannelID.ValueString(), discord.Payload{
		"content":          cfg.Content.ValueString(),
		"allowed_mentions": map[string]any{"parse": parse},
	})
	if err != nil {
		apiError(&resp.Diagnostics, "send message", err)
		return
	}
	progress(resp, "Posted message %s", msg.ID)
}

// messageActionModel identifies a message for the actions that change one.
type messageActionModel struct {
	ChannelID types.String `tfsdk:"channel_id"`
	MessageID types.String `tfsdk:"message_id"`
}

func messageActionSchema(desc, channel string) schema.Schema {
	return schema.Schema{
		MarkdownDescription: desc,
		Attributes: map[string]schema.Attribute{
			"channel_id": actionID("ID of the " + channel + " the message is in."),
			"message_id": actionID("ID of the message."),
		},
	}
}

type crosspostMessageAction struct{ actionClient }

func newCrosspostMessageAction() action.Action { return &crosspostMessageAction{} }

func (a *crosspostMessageAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_crosspost_message"
}

func (a *crosspostMessageAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = messageActionSchema("Publishes a message in an announcement channel to every channel following "+
		"it. A message can be published once. Requires the Send Messages permission for the bot's own messages, "+
		"and also Manage Messages for other messages.", "announcement channel")
}

func (a *crosspostMessageAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var cfg messageActionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := a.client.CrosspostMessage(ctx, cfg.ChannelID.ValueString(), cfg.MessageID.ValueString()); err != nil {
		apiError(&resp.Diagnostics, "crosspost message", err)
		return
	}
	progress(resp, "Published message %s to following channels", cfg.MessageID.ValueString())
}

type endPollAction struct{ actionClient }

func newEndPollAction() action.Action { return &endPollAction{} }

func (a *endPollAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_end_poll"
}

func (a *endPollAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = messageActionSchema("Ends a poll immediately. Only polls the bot posted can be ended.", "channel")
}

func (a *endPollAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var cfg messageActionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := a.client.EndPoll(ctx, cfg.ChannelID.ValueString(), cfg.MessageID.ValueString()); err != nil {
		apiError(&resp.Diagnostics, "end poll", err)
		return
	}
	progress(resp, "Ended poll %s", cfg.MessageID.ValueString())
}

// bulkDeleteMaxAge is how old a message can be for Discord to bulk delete
// it.
const bulkDeleteMaxAge = 14 * 24 * time.Hour

type bulkDeleteMessagesAction struct{ actionClient }

type bulkDeleteMessagesModel struct {
	ChannelID      types.String `tfsdk:"channel_id"`
	MessageIDs     types.Set    `tfsdk:"message_ids"`
	AuditLogReason types.String `tfsdk:"audit_log_reason"`
}

func newBulkDeleteMessagesAction() action.Action { return &bulkDeleteMessagesAction{} }

func (a *bulkDeleteMessagesAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bulk_delete_messages"
}

func (a *bulkDeleteMessagesAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Deletes between 2 and 100 messages from a channel in one request. Discord refuses the " +
			"whole request if any message is more than two weeks old, so the action checks the age each message ID " +
			"encodes before sending it. IDs of messages that no longer exist still count toward the limits. " +
			"Requires the Manage Messages permission.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": actionAuditLogReason(),
			"channel_id":       actionID("ID of the channel the messages are in."),
			"message_ids": actionIDSet("IDs of the messages to delete, between 2 and 100, each less than two "+
				"weeks old.", 2, 100, recentMessagesValidator{}),
		},
	}
}

// oldMessages returns the IDs that are too old to bulk delete at now.
func oldMessages(ids []string, now time.Time) []string {
	var old []string
	for _, id := range ids {
		if t, ok := snowflakeTime(id); ok && now.Sub(t) > bulkDeleteMaxAge {
			old = append(old, id)
		}
	}
	slices.Sort(old)
	return old
}

func addOldMessagesError(diags *diag.Diagnostics, p path.Path, old []string) {
	diags.AddAttributeError(p, "Messages too old to bulk delete",
		fmt.Sprintf("Discord only bulk deletes messages less than two weeks old; these are older: %v.", old))
}

// recentMessagesValidator rejects message IDs older than Discord bulk
// deletes, so the plan fails rather than the apply.
type recentMessagesValidator struct{}

func (v recentMessagesValidator) Description(context.Context) string {
	return "each message must be less than two weeks old"
}

func (v recentMessagesValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v recentMessagesValidator) ValidateSet(ctx context.Context, req validator.SetRequest, resp *validator.SetResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var ids []types.String
	resp.Diagnostics.Append(req.ConfigValue.ElementsAs(ctx, &ids, false)...)
	known := make([]string, 0, len(ids))
	for _, id := range ids {
		if !id.IsUnknown() {
			known = append(known, id.ValueString())
		}
	}
	if old := oldMessages(known, time.Now()); len(old) > 0 {
		addOldMessagesError(&resp.Diagnostics, req.Path, old)
	}
}

func (a *bulkDeleteMessagesAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var cfg bulkDeleteMessagesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var ids []string
	resp.Diagnostics.Append(cfg.MessageIDs.ElementsAs(ctx, &ids, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Messages planned while recent can have aged by the time a saved plan
	// is applied.
	if old := oldMessages(ids, time.Now()); len(old) > 0 {
		addOldMessagesError(&resp.Diagnostics, path.Root("message_ids"), old)
		return
	}
	ctx = withAuditLogReason(ctx, cfg.AuditLogReason)
	if err := a.client.BulkDeleteMessages(ctx, cfg.ChannelID.ValueString(), ids); err != nil {
		apiError(&resp.Diagnostics, "bulk delete messages", err)
		return
	}
	progress(resp, "Deleted %d messages", len(ids))
}
