package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &stageInstanceResource{}
	_ resource.ResourceWithImportState = &stageInstanceResource{}
	_ resource.ResourceWithIdentity    = &stageInstanceResource{}
)

type stageInstanceResource struct {
	resourceIdentity
	client *discord.Client
}

type stageInstanceModel struct {
	ID                    types.String `tfsdk:"id"`
	ChannelID             types.String `tfsdk:"channel_id"`
	ServerID              types.String `tfsdk:"server_id"`
	Topic                 types.String `tfsdk:"topic"`
	SendStartNotification types.Bool   `tfsdk:"send_start_notification"`
	ScheduledEventID      types.String `tfsdk:"scheduled_event_id"`
	AuditLogReason        types.String `tfsdk:"audit_log_reason"`
}

func newStageInstanceResource() resource.Resource {
	return &stageInstanceResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		channelIdentity("channel_id"),
	}}}
}

func (r *stageInstanceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_stage_instance"
}

func (r *stageInstanceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Opens a stage channel, making it live with a topic visible to server members. Destroying " +
			"the resource ends the stage.\n\n" +
			"Discord closes a stage on its own a few minutes after it has no speakers, after which the next plan opens " +
			"it again. This makes the resource suited to automating events, such as opening a stage shortly before it " +
			"starts, rather than to long-lived configuration.\n\n" +
			"The bot must be a stage moderator, with the Manage Channels, Mute Members and Move Members permissions on " +
			"the channel.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("Stage instance ID. A stage that is closed and opened again gets a new ID."),
			"channel_id": schema.StringAttribute{
				MarkdownDescription: "ID of the stage channel. A channel has at most one open stage.",
				Required:            true,
				Validators:          []validator.String{snowflakeValidator()},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"server_id": schema.StringAttribute{
				MarkdownDescription: "ID of the server the stage channel belongs to.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"topic": schema.StringAttribute{
				MarkdownDescription: "Topic of the stage (1-120 characters).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 120)},
			},
			"send_start_notification": schema.BoolAttribute{
				MarkdownDescription: "Whether to notify @everyone that the stage has started. Only used when the stage " +
					"is opened, and only sent if the bot also has the Mention @everyone permission. Changing it later " +
					"updates state without calling Discord.",
				Optional: true,
			},
			"scheduled_event_id": schema.StringAttribute{
				MarkdownDescription: "ID of the scheduled event the stage is for. Discord only accepts it when the stage " +
					"is opened, so changing it closes the stage and opens it again. Opening the stage starts the event, and closing " +
					"it completes the event, which a `discord_scheduled_event` then removes from state.",
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{snowflakeValidator()},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
		},
	}
}

func (r *stageInstanceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *stageInstanceModel) apply(s *discord.StageInstance) {
	m.ID = types.StringValue(s.ID)
	m.ChannelID = types.StringValue(s.ChannelID)
	m.ServerID = types.StringValue(s.GuildID)
	m.Topic = types.StringValue(s.Topic)
	m.ScheduledEventID = stringPtrValue(s.GuildScheduledEventID)
}

func (r *stageInstanceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan stageInstanceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	p := discord.Payload{
		"channel_id":    plan.ChannelID.ValueString(),
		"topic":         plan.Topic.ValueString(),
		"privacy_level": discord.PrivacyLevelGuildOnly,
	}
	putBool(p, "send_start_notification", plan.SendStartNotification)
	putKnownString(p, "guild_scheduled_event_id", plan.ScheduledEventID)
	s, err := r.client.CreateStageInstance(ctx, p)
	if err != nil {
		apiError(&resp.Diagnostics, "create stage instance", err)
		return
	}
	plan.apply(s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *stageInstanceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state stageInstanceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.client.GetStageInstance(ctx, state.ChannelID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read stage instance", err)
		return
	}
	state.apply(s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *stageInstanceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan, state stageInstanceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Topic.Equal(state.Topic) {
		// Only send_start_notification changed, which Discord reads only
		// when the stage is opened.
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	s, err := r.client.ModifyStageInstance(ctx, state.ChannelID.ValueString(), discord.Payload{"topic": plan.Topic.ValueString()})
	if err != nil {
		apiError(&resp.Diagnostics, "update stage instance", err)
		return
	}
	plan.apply(s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *stageInstanceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state stageInstanceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	if err := r.client.DeleteStageInstance(ctx, state.ChannelID.ValueString()); err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete stage instance", err)
	}
}
