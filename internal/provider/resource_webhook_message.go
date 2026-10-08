package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &webhookMessageResource{}
	_ resource.ResourceWithImportState = &webhookMessageResource{}
	_ resource.ResourceWithIdentity    = &webhookMessageResource{}
)

type webhookMessageResource struct {
	resourceIdentity
	client *discord.Client
}

type webhookMessageModel struct {
	ID              types.String `tfsdk:"id"`
	WebhookID       types.String `tfsdk:"webhook_id"`
	ThreadID        types.String `tfsdk:"thread_id"`
	ThreadName      types.String `tfsdk:"thread_name"`
	ChannelID       types.String `tfsdk:"channel_id"`
	Content         types.String `tfsdk:"content"`
	Embeds          types.List   `tfsdk:"embeds"`
	AllowedMentions types.Set    `tfsdk:"allowed_mentions"`
	Username        types.String `tfsdk:"username"`
	AvatarURL       types.String `tfsdk:"avatar_url"`
}

func newWebhookMessageResource() resource.Resource {
	return &webhookMessageResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		{name: "webhook_id", description: "ID of the webhook that posted the message.", state: []string{"webhook_id"}},
		{name: "channel_id", description: "ID of the channel or thread the message is in.", state: []string{"channel_id"}},
		{name: "message_id", description: "ID of the message.", state: []string{"id"}},
	}}}
}

func (r *webhookMessageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook_message"
}

func (r *webhookMessageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a message posted through an incoming webhook, shown with the webhook's name and " +
			"avatar or the per-message `username` and `avatar_url`. Edits are applied in place. If the message is " +
			"deleted outside Terraform it is posted again on the next apply.\n\n" +
			"The webhook token is never stored: every operation reads it with the bot token from Get Webhook, which " +
			"requires the Manage Webhooks permission in the webhook's channel unless the bot's application created " +
			"the webhook. This lets `discord_webhook` keep `store_secrets = false`.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("Message ID."),
			"webhook_id": schema.StringAttribute{
				MarkdownDescription: "ID of the incoming webhook to post with.",
				Required:            true,
				Validators:          []validator.String{snowflakeValidator()},
				PlanModifiers:       replace,
			},
			"thread_id": schema.StringAttribute{
				MarkdownDescription: "ID of a thread in the webhook's channel to post in, such as an existing forum post. " +
					"Discord unarchives the thread.",
				Optional:      true,
				Validators:    []validator.String{snowflakeValidator(), stringvalidator.ConflictsWith(path.MatchRoot("thread_name"))},
				PlanModifiers: replace,
			},
			"thread_name": schema.StringAttribute{
				MarkdownDescription: "Name of a post to start with this message (1-100 characters). Only for webhooks in a " +
					"forum or media channel, which require `thread_id` or `thread_name`. Destroying the resource deletes " +
					"the message only, not the post.",
				Optional:      true,
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 100)},
				PlanModifiers: replace,
			},
			"channel_id": schema.StringAttribute{
				MarkdownDescription: "ID of the channel or thread the message is in.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"content": schema.StringAttribute{
				MarkdownDescription: "Message text (up to 2000 characters). At least one of `content` or `embeds` is required.",
				Optional:            true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 2000),
					stringvalidator.AtLeastOneOf(path.MatchRoot("embeds")),
				},
			},
			"embeds":           embedsAttribute(),
			"allowed_mentions": allowedMentionsAttribute(),
			"username": schema.StringAttribute{
				MarkdownDescription: "Name to show instead of the webhook's name (1-80 characters). Discord cannot change it " +
					"after posting, so changing it posts a new message.",
				Optional:      true,
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 80)},
				PlanModifiers: replace,
			},
			"avatar_url": schema.StringAttribute{
				MarkdownDescription: "URL of an avatar to show instead of the webhook's avatar. Changing it posts a new message.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 2048)},
				PlanModifiers:       replace,
			},
		},
	}
}

func (r *webhookMessageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

// ImportState imports a message posted in a thread with thread_id set to
// the thread, which the import ID's channel_id names.
func (r *webhookMessageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.resourceIdentity.ImportState(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		return
	}
	var webhookID, channelID types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("webhook_id"), &webhookID)...)
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("channel_id"), &channelID)...)
	if resp.Diagnostics.HasError() {
		return
	}
	w, err := r.client.GetWebhook(ctx, webhookID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read webhook", err)
		return
	}
	if w.ChannelID != channelID.ValueString() {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("thread_id"), channelID)...)
	}
}

// token reads the webhook's token with the bot token, so that it never has
// to be stored.
func (r *webhookMessageResource) token(ctx context.Context, webhookID string) (string, error) {
	w, err := r.client.GetWebhook(ctx, webhookID)
	if err != nil {
		return "", err
	}
	if w.Token == "" {
		return "", fmt.Errorf("webhook %s has no token: only incoming webhooks can post messages", webhookID)
	}
	return w.Token, nil
}

// thread is the thread_id query parameter for requests about the posted
// message: the thread it was posted in, or empty for the webhook's channel.
func (m *webhookMessageModel) thread() string {
	if !m.ThreadName.IsNull() {
		return m.ChannelID.ValueString()
	}
	return m.ThreadID.ValueString()
}

func (m *webhookMessageModel) message() *messageModel {
	return &messageModel{Content: m.Content, Embeds: m.Embeds, AllowedMentions: m.AllowedMentions}
}

func (m *webhookMessageModel) apply(ctx context.Context, msg *discord.Message) diag.Diagnostics {
	mm := m.message()
	diags := mm.apply(ctx, msg)
	m.ID = types.StringValue(msg.ID)
	m.ChannelID = types.StringValue(msg.ChannelID)
	m.Content, m.Embeds = mm.Content, mm.Embeds
	return diags
}

func (r *webhookMessageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan webhookMessageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, diags := plan.message().payload(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	putKnownString(p, "username", plan.Username)
	putKnownString(p, "avatar_url", plan.AvatarURL)
	putKnownString(p, "thread_name", plan.ThreadName)
	token, err := r.token(ctx, plan.WebhookID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read webhook token", err)
		return
	}
	msg, err := r.client.ExecuteWebhook(ctx, plan.WebhookID.ValueString(), token, plan.ThreadID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "execute webhook", err)
		return
	}
	resp.Diagnostics.Append(plan.apply(ctx, msg)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookMessageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state webhookMessageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	token, err := r.token(ctx, state.WebhookID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read webhook token", err)
		return
	}
	msg, err := r.client.GetWebhookMessage(ctx, state.WebhookID.ValueString(), token, state.ID.ValueString(), state.thread())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read webhook message", err)
		return
	}
	resp.Diagnostics.Append(state.apply(ctx, msg)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *webhookMessageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan, state webhookMessageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desired, diags := plan.message().payload(ctx)
	resp.Diagnostics.Append(diags...)
	current, diags := state.message().payload(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := diffPayload(desired, current)
	// Without allowed_mentions, Discord parses mentions in edited content
	// with its defaults and pings @everyone and roles.
	body["allowed_mentions"] = desired["allowed_mentions"]
	token, err := r.token(ctx, state.WebhookID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read webhook token", err)
		return
	}
	msg, err := r.client.EditWebhookMessage(ctx, state.WebhookID.ValueString(), token, state.ID.ValueString(), state.thread(), body)
	if err != nil {
		apiError(&resp.Diagnostics, "edit webhook message", err)
		return
	}
	resp.Diagnostics.Append(plan.apply(ctx, msg)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookMessageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state webhookMessageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A deleted webhook can no longer delete its messages.
	token, err := r.token(ctx, state.WebhookID.ValueString())
	if discord.IsNotFound(err) {
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read webhook token", err)
		return
	}
	err = r.client.DeleteWebhookMessage(ctx, state.WebhookID.ValueString(), token, state.ID.ValueString(), state.thread())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete webhook message", err)
	}
}
