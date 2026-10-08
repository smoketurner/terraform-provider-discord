package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// webhookTypeChannelFollower is the type of the webhooks Discord creates
// when a channel follows an announcement channel.
const webhookTypeChannelFollower = 2

var (
	_ resource.ResourceWithConfigure   = &channelFollowerResource{}
	_ resource.ResourceWithImportState = &channelFollowerResource{}
	_ resource.ResourceWithIdentity    = &channelFollowerResource{}
)

type channelFollowerResource struct {
	resourceIdentity
	client *discord.Client
}

type channelFollowerModel struct {
	ID              types.String `tfsdk:"id"`
	SourceChannelID types.String `tfsdk:"source_channel_id"`
	ChannelID       types.String `tfsdk:"channel_id"`
	AuditLogReason  types.String `tfsdk:"audit_log_reason"`
}

func newChannelFollowerResource() resource.Resource {
	return &channelFollowerResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		{name: "webhook_id", description: "ID of the Channel Follower webhook.", state: []string{"id"}},
	}}}
}

func (r *channelFollowerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_channel_follower"
}

func (r *channelFollowerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	snowflake := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: desc,
			Required:            true,
			Validators:          []validator.String{snowflakeValidator()},
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Follows an announcement channel, possibly in another server, so that its published " +
			"messages are posted into a channel of this server. Discord implements this as a Channel Follower webhook " +
			"in the target channel; deleting the resource deletes the webhook, which unfollows. Requires the Manage " +
			"Webhooks permission in the target channel. If the webhook is deleted outside Terraform the channel is " +
			"followed again on the next apply.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason":  auditLogReasonAttribute(),
			"id":                idAttribute("ID of the Channel Follower webhook."),
			"source_channel_id": snowflake("ID of the announcement channel to follow."),
			"channel_id": snowflake("ID of the text or announcement channel that receives the messages. Moving the " +
				"webhook to another channel outside Terraform makes the next plan follow again into this channel."),
		},
	}
}

func (r *channelFollowerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (r *channelFollowerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan channelFollowerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	f, err := r.client.FollowChannel(ctx, plan.SourceChannelID.ValueString(), plan.ChannelID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "follow channel", err)
		return
	}
	plan.ID = types.StringValue(f.WebhookID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *channelFollowerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state channelFollowerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	w, err := r.client.GetFollowerWebhook(ctx, state.ID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read channel follower webhook", err)
		return
	}
	if w.Type != webhookTypeChannelFollower {
		resp.Diagnostics.AddError("Not a channel follower",
			fmt.Sprintf("Webhook %s has type %d, not a Channel Follower webhook (type %d). Use discord_webhook for other webhooks.",
				w.ID, w.Type, webhookTypeChannelFollower))
		return
	}
	state.ChannelID = types.StringValue(w.ChannelID)
	// Discord omits the source once the bot loses access to the followed
	// server; the followed channel cannot change, so state keeps it.
	if w.SourceChannel != nil {
		state.SourceChannelID = types.StringValue(w.SourceChannel.ID)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *channelFollowerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	resp.Diagnostics.AddError("Unexpected update", "All discord_channel_follower attributes except audit_log_reason force replacement.")
}

func (r *channelFollowerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state channelFollowerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	if err := r.client.DeleteWebhook(ctx, state.ID.ValueString()); err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete channel follower webhook", err)
	}
}
