package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

const webhookURLPrefix = "https://discord.com/api/webhooks/"

var dataURIRegexp = regexp.MustCompile(`^data:image/(png|jpeg|gif|webp);base64,[A-Za-z0-9+/=]+$`)

func dataURIValidator() validator.String {
	return stringvalidator.RegexMatches(dataURIRegexp,
		`must be a base64 image data URI, e.g. "data:image/png;base64,${filebase64("image.png")}"`)
}

var (
	_ resource.ResourceWithConfigure   = &webhookResource{}
	_ resource.ResourceWithImportState = &webhookResource{}
)

type webhookResource struct {
	client *discord.Client
}

type webhookModel struct {
	ID         types.String `tfsdk:"id"`
	ChannelID  types.String `tfsdk:"channel_id"`
	Name       types.String `tfsdk:"name"`
	Avatar     types.String `tfsdk:"avatar"`
	AvatarHash types.String `tfsdk:"avatar_hash"`
	ServerID   types.String `tfsdk:"server_id"`
	Token      types.String `tfsdk:"token"`
	URL        types.String `tfsdk:"url"`
}

func newWebhookResource() resource.Resource { return &webhookResource{} }

func (r *webhookResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook"
}

func (r *webhookResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a channel webhook. The webhook URL and token are secrets and are stored in Terraform state.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("Webhook ID."),
			"channel_id": schema.StringAttribute{
				MarkdownDescription: "ID of the channel the webhook posts to. Changing it moves the webhook.",
				Required:            true,
				Validators:          []validator.String{snowflakeValidator()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Default name of the webhook (1-80 characters, must not contain \"clyde\" or \"discord\").",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 80),
					notContainsValidator{"clyde", "discord"},
				},
			},
			"avatar": schema.StringAttribute{
				MarkdownDescription: "Default avatar as a data URI, e.g. `\"data:image/png;base64,${filebase64(\"avatar.png\")}\"`.",
				Optional:            true,
				Validators:          []validator.String{dataURIValidator()},
			},
			"avatar_hash": schema.StringAttribute{
				MarkdownDescription: "Hash of the current avatar.",
				Computed:            true,
			},
			"server_id": schema.StringAttribute{
				MarkdownDescription: "ID of the server the webhook belongs to.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "Secure token of the webhook.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       keep,
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "URL for executing the webhook.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       keep,
			},
		},
	}
}

func (r *webhookResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *webhookModel) apply(w *discord.Webhook) {
	m.ID = types.StringValue(w.ID)
	m.ChannelID = types.StringValue(w.ChannelID)
	m.Name = stringPtrValue(w.Name)
	m.AvatarHash = stringPtrValue(w.Avatar)
	m.ServerID = types.StringValue(w.GuildID)
	if w.Token != "" {
		m.Token = types.StringValue(w.Token)
		m.URL = types.StringValue(webhookURLPrefix + w.ID + "/" + w.Token)
	}
}

func (r *webhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := discord.Payload{"name": plan.Name.ValueString()}
	putKnownString(p, "avatar", plan.Avatar)
	w, err := r.client.CreateWebhook(ctx, plan.ChannelID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "create webhook", err)
		return
	}
	plan.apply(w)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	w, err := r.client.GetWebhook(ctx, state.ID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read webhook", err)
		return
	}
	state.apply(w)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *webhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desired, current := discord.Payload{}, discord.Payload{}
	for p, m := range map[*discord.Payload]*webhookModel{&desired: &plan, &current: &state} {
		putString(*p, "name", m.Name)
		putString(*p, "channel_id", m.ChannelID)
		putString(*p, "avatar", m.Avatar)
	}
	w, err := r.client.ModifyWebhook(ctx, state.ID.ValueString(), diffPayload(desired, current))
	if err != nil {
		apiError(&resp.Diagnostics, "update webhook", err)
		return
	}
	plan.Token, plan.URL = state.Token, state.URL
	plan.apply(w)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteWebhook(ctx, state.ID.ValueString()); err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete webhook", err)
	}
}

func (r *webhookResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
