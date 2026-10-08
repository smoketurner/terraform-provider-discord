package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
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
	_ resource.ResourceWithIdentity    = &webhookResource{}
)

type webhookResource struct {
	resourceIdentity
	client *discord.Client
}

type webhookModel struct {
	ID              types.String `tfsdk:"id"`
	ChannelID       types.String `tfsdk:"channel_id"`
	Name            types.String `tfsdk:"name"`
	Avatar          types.String `tfsdk:"avatar"`
	AvatarWO        types.String `tfsdk:"avatar_wo"`
	AvatarWOVersion types.Int64  `tfsdk:"avatar_wo_version"`
	AvatarHash      types.String `tfsdk:"avatar_hash"`
	ServerID        types.String `tfsdk:"server_id"`
	Token           types.String `tfsdk:"token"`
	URL             types.String `tfsdk:"url"`
	StoreSecrets    types.Bool   `tfsdk:"store_secrets"`
	AuditLogReason  types.String `tfsdk:"audit_log_reason"`
}

func newWebhookResource() resource.Resource {
	return &webhookResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		{name: "webhook_id", description: "ID of the webhook.", state: []string{"id"}},
	}}}
}

func (r *webhookResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook"
}

func (r *webhookResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a channel webhook. The webhook URL and token are secrets. They are stored in " +
			"Terraform state unless `store_secrets` is `false`; the `discord_webhook` ephemeral resource (Terraform 1.10 " +
			"or later) reads them without storing them.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("Webhook ID."),
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
				MarkdownDescription: "Default avatar as a data URI, e.g. `\"data:image/png;base64,${filebase64(\"avatar.png\")}\"`. " +
					"Stored in state; prefer `avatar_wo` on Terraform 1.11 or later.",
				Optional:   true,
				Validators: []validator.String{dataURIValidator(), stringvalidator.ConflictsWith(path.MatchRoot("avatar_wo"))},
			},
			"avatar_wo": writeOnlyImage("Default avatar as a data URI.", "avatar"),
			"avatar_wo_version": writeOnlyVersion("avatar",
				"Setting or changing it uploads `avatar_wo`; removing it removes the avatar unless `avatar` is set."),
			"avatar_hash": schema.StringAttribute{
				MarkdownDescription: "Hash of the current avatar. A change made outside Terraform makes the next plan upload " +
					"the configured avatar again.",
				Computed: true,
			},
			"server_id": schema.StringAttribute{
				MarkdownDescription: "ID of the server the webhook belongs to.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"store_secrets": schema.BoolAttribute{
				MarkdownDescription: "Whether to store `token` and `url` in Terraform state. Set it to `false` to keep " +
					"them out of state, and read them with the `discord_webhook` ephemeral resource instead. Defaults to " +
					"`true`. An imported webhook starts with `false`, so the import itself never stores the secrets; with " +
					"`true` in the configuration, the next apply stores them.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "Secure token of the webhook. Null when `store_secrets` is `false`.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{storedSecret{}},
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "URL for executing the webhook. Null when `store_secrets` is `false`.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{storedSecret{}},
			},
		},
	}
}

func (r *webhookResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

// storedSecret plans the token and URL: null when store_secrets is false,
// otherwise the stored value, or unknown until the apply that stores it.
type storedSecret struct{}

func (storedSecret) Description(context.Context) string {
	return "Null unless store_secrets is true."
}

func (m storedSecret) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (storedSecret) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var store types.Bool
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("store_secrets"), &store)...)
	switch {
	case store.IsUnknown():
		resp.PlanValue = types.StringUnknown()
	case !store.ValueBool():
		resp.PlanValue = types.StringNull()
	case !req.StateValue.IsNull():
		resp.PlanValue = req.StateValue
	default:
		resp.PlanValue = types.StringUnknown()
	}
}

func (m *webhookModel) apply(w *discord.Webhook) {
	m.ID = types.StringValue(w.ID)
	m.ChannelID = types.StringValue(w.ChannelID)
	m.Name = stringPtrValue(w.Name)
	m.AvatarHash = stringPtrValue(w.Avatar)
	m.ServerID = types.StringValue(w.GuildID)
	m.Token, m.URL = types.StringNull(), types.StringNull()
	if m.StoreSecrets.ValueBool() && w.Token != "" {
		m.Token = types.StringValue(w.Token)
		m.URL = types.StringValue(webhookURL(w))
	}
}

func webhookURL(w *discord.Webhook) string {
	return webhookURLPrefix + w.ID + "/" + w.Token
}

// ImportState imports with store_secrets false, so that importing never
// writes the token to state.
func (r *webhookResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.resourceIdentity.ImportState(ctx, req, resp)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("store_secrets"), false)...)
	}
}

func (r *webhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	p := discord.Payload{"name": plan.Name.ValueString()}
	putKnownString(p, "avatar", plan.Avatar)
	if !plan.AvatarWOVersion.IsNull() {
		putKnownString(p, "avatar", writeOnlyString(ctx, req.Config, "avatar_wo", &resp.Diagnostics))
	}
	if resp.Diagnostics.HasError() {
		return
	}
	w, err := r.client.CreateWebhook(ctx, plan.ChannelID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "create webhook", err)
		return
	}
	plan.apply(w)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
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
	clearImageOnDrift(state.AvatarHash, w.Avatar, &state.Avatar, &state.AvatarWOVersion)
	// State written before store_secrets existed holds the secrets.
	if state.StoreSecrets.IsNull() {
		state.StoreSecrets = types.BoolValue(true)
	}
	state.apply(w)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *webhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan, state webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	desired, current := discord.Payload{}, discord.Payload{}
	for p, m := range map[*discord.Payload]*webhookModel{&desired: &plan, &current: &state} {
		putString(*p, "name", m.Name)
		putString(*p, "channel_id", m.ChannelID)
		// avatar_wo is compared through its version below.
		if m.AvatarWOVersion.IsNull() {
			putString(*p, "avatar", m.Avatar)
		}
	}
	payload := diffPayload(desired, current)
	if writeOnlyChanged(plan.AvatarWOVersion, state.AvatarWOVersion) {
		putKnownString(payload, "avatar", writeOnlyString(ctx, req.Config, "avatar_wo", &resp.Diagnostics))
	}
	if resp.Diagnostics.HasError() {
		return
	}
	var w *discord.Webhook
	var err error
	if len(payload) > 0 {
		w, err = r.client.ModifyWebhook(ctx, state.ID.ValueString(), payload)
		if err != nil {
			apiError(&resp.Diagnostics, "update webhook", err)
			return
		}
	}
	// A change to store_secrets alone, or a response without the token,
	// needs the webhook read back.
	if w == nil || (plan.StoreSecrets.ValueBool() && w.Token == "") {
		w, err = r.client.GetWebhook(ctx, state.ID.ValueString())
		if err != nil {
			apiError(&resp.Diagnostics, "read webhook", err)
			return
		}
	}
	plan.apply(w)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	if err := r.client.DeleteWebhook(ctx, state.ID.ValueString()); err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete webhook", err)
	}
}
