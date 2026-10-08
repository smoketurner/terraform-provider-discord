package provider

import (
	"context"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

const featureWelcomeScreenEnabled = "WELCOME_SCREEN_ENABLED"

var (
	_ resource.ResourceWithConfigure   = &welcomeScreenResource{}
	_ resource.ResourceWithImportState = &welcomeScreenResource{}
	_ resource.ResourceWithIdentity    = &welcomeScreenResource{}
)

type welcomeScreenResource struct {
	resourceIdentity
	client *discord.Client
}

type welcomeScreenModel struct {
	ID              types.String `tfsdk:"id"`
	ServerID        types.String `tfsdk:"server_id"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	Description     types.String `tfsdk:"description"`
	WelcomeChannels types.List   `tfsdk:"welcome_channels"`
	AuditLogReason  types.String `tfsdk:"audit_log_reason"`
}

type welcomeChannelModel struct {
	ChannelID   types.String `tfsdk:"channel_id"`
	Description types.String `tfsdk:"description"`
	EmojiID     types.String `tfsdk:"emoji_id"`
	EmojiName   types.String `tfsdk:"emoji_name"`
}

var welcomeChannelAttrTypes = map[string]attr.Type{
	"channel_id": types.StringType, "description": types.StringType,
	"emoji_id": types.StringType, "emoji_name": types.StringType,
}

func newWelcomeScreenResource() resource.Resource {
	return &welcomeScreenResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{serverIdentity("server_id", "id")}}}
}

func (r *welcomeScreenResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_welcome_screen"
}

func (r *welcomeScreenResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the welcome screen shown to new members of a Community server. Each server has one " +
			"welcome screen, so destroying this resource disables it and leaves its description and channels unchanged. " +
			"Requires Community and the Manage Server permission.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("Server ID."),
			"server_id":        serverIDAttribute(),
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the welcome screen is shown.",
				Required:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Server description shown on the welcome screen (up to 140 characters).",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 140)},
			},
			"welcome_channels": schema.ListNestedAttribute{
				MarkdownDescription: "Channels shown on the welcome screen, in order (at most 5).",
				Optional:            true,
				Validators:          []validator.List{listvalidator.SizeAtMost(5)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"channel_id": schema.StringAttribute{
							MarkdownDescription: "ID of the channel.",
							Required:            true,
							Validators:          []validator.String{snowflakeValidator()},
						},
						"description": schema.StringAttribute{
							MarkdownDescription: "Description shown for the channel (up to 50 characters).",
							Required:            true,
							Validators:          []validator.String{stringvalidator.LengthBetween(1, 50)},
						},
						"emoji_id": schema.StringAttribute{
							MarkdownDescription: "ID of a custom server emoji shown for the channel.",
							Optional:            true,
							Validators:          []validator.String{snowflakeValidator()},
						},
						"emoji_name": schema.StringAttribute{
							MarkdownDescription: "Unicode emoji shown for the channel, or the name of the custom emoji in `emoji_id`.",
							Optional:            true,
						},
					},
				},
			},
		},
	}
}

func (r *welcomeScreenResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *welcomeScreenModel) payload(ctx context.Context) (discord.Payload, diag.Diagnostics) {
	var channels []welcomeChannelModel
	diags := m.WelcomeChannels.ElementsAs(ctx, &channels, false)
	out := make([]discord.WelcomeScreenChannel, 0, len(channels))
	for _, c := range channels {
		out = append(out, discord.WelcomeScreenChannel{
			ChannelID:   c.ChannelID.ValueString(),
			Description: c.Description.ValueString(),
			EmojiID:     c.EmojiID.ValueStringPointer(),
			EmojiName:   c.EmojiName.ValueStringPointer(),
		})
	}
	p := discord.Payload{"welcome_channels": out}
	putBool(p, "enabled", m.Enabled)
	putString(p, "description", m.Description)
	return p, diags
}

// apply stores the welcome screen read from Discord. Discord names a custom
// emoji even when only its ID was sent, so the name is kept only for
// channels whose prior state had one.
func (m *welcomeScreenModel) apply(ctx context.Context, ws *discord.WelcomeScreen) diag.Diagnostics {
	var prior []welcomeChannelModel
	diags := m.WelcomeChannels.ElementsAs(ctx, &prior, false)
	channels := make([]welcomeChannelModel, 0, len(ws.WelcomeChannels))
	for _, c := range ws.WelcomeChannels {
		ch := welcomeChannelModel{
			ChannelID:   types.StringValue(c.ChannelID),
			Description: types.StringValue(c.Description),
			EmojiID:     stringPtrValue(c.EmojiID),
			EmojiName:   stringPtrValue(c.EmojiName),
		}
		i := slices.IndexFunc(prior, func(p welcomeChannelModel) bool { return p.ChannelID.ValueString() == c.ChannelID })
		if c.EmojiID != nil && (i < 0 || prior[i].EmojiName.IsNull()) {
			ch.EmojiName = types.StringNull()
		}
		channels = append(channels, ch)
	}
	m.ID = m.ServerID
	m.Description = stringPtrValue(ws.Description)
	if len(channels) == 0 && m.WelcomeChannels.IsNull() {
		return diags
	}
	list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: welcomeChannelAttrTypes}, channels)
	diags.Append(d...)
	m.WelcomeChannels = list
	return diags
}

func (r *welcomeScreenResource) write(ctx context.Context, m *welcomeScreenModel, diags *diag.Diagnostics) {
	p, d := m.payload(ctx)
	diags.Append(d...)
	if diags.HasError() {
		return
	}
	ws, err := r.client.ModifyWelcomeScreen(ctx, m.ServerID.ValueString(), p)
	if err != nil {
		apiError(diags, "update welcome screen", err)
		return
	}
	diags.Append(m.apply(ctx, ws)...)
}

func (r *welcomeScreenResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan welcomeScreenModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	if r.write(ctx, &plan, &resp.Diagnostics); resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *welcomeScreenResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state welcomeScreenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetGuild(ctx, state.ServerID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read server", err)
		return
	}
	// Discord answers 404 for a server whose welcome screen was never set.
	ws, err := r.client.GetWelcomeScreen(ctx, state.ServerID.ValueString())
	if discord.IsNotFound(err) {
		ws, err = &discord.WelcomeScreen{}, nil
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read welcome screen", err)
		return
	}
	state.Enabled = types.BoolValue(slices.Contains(g.Features, featureWelcomeScreenEnabled))
	resp.Diagnostics.Append(state.apply(ctx, ws)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *welcomeScreenResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan welcomeScreenModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	if r.write(ctx, &plan, &resp.Diagnostics); resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *welcomeScreenResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state welcomeScreenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	_, err := r.client.ModifyWelcomeScreen(ctx, state.ServerID.ValueString(), discord.Payload{"enabled": false})
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "disable welcome screen", err)
	}
}
