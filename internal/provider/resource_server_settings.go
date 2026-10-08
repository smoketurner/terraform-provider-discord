package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	verificationLevels    = enumMapping{"none", "low", "medium", "high", "very_high"}
	messageNotifications  = enumMapping{"all_messages", "only_mentions"}
	explicitContentFilter = enumMapping{"disabled", "members_without_roles", "all_members"}
)

var (
	_ resource.ResourceWithConfigure   = &serverSettingsResource{}
	_ resource.ResourceWithImportState = &serverSettingsResource{}
	_ resource.ResourceWithIdentity    = &serverSettingsResource{}
)

type serverSettingsResource struct {
	resourceIdentity
	client *discord.Client
}

type serverSettingsModel struct {
	ID                          types.String `tfsdk:"id"`
	ServerID                    types.String `tfsdk:"server_id"`
	Name                        types.String `tfsdk:"name"`
	Description                 types.String `tfsdk:"description"`
	Icon                        types.String `tfsdk:"icon"`
	IconWO                      types.String `tfsdk:"icon_wo"`
	IconWOVersion               types.Int64  `tfsdk:"icon_wo_version"`
	IconHash                    types.String `tfsdk:"icon_hash"`
	VerificationLevel           types.String `tfsdk:"verification_level"`
	DefaultMessageNotifications types.String `tfsdk:"default_message_notifications"`
	ExplicitContentFilter       types.String `tfsdk:"explicit_content_filter"`
	AFKChannelID                types.String `tfsdk:"afk_channel_id"`
	AFKTimeout                  types.Int64  `tfsdk:"afk_timeout"`
	SystemChannelID             types.String `tfsdk:"system_channel_id"`
	SystemChannelFlags          types.Int64  `tfsdk:"system_channel_flags"`
	RulesChannelID              types.String `tfsdk:"rules_channel_id"`
	PublicUpdatesChannelID      types.String `tfsdk:"public_updates_channel_id"`
	SafetyAlertsChannelID       types.String `tfsdk:"safety_alerts_channel_id"`
	PreferredLocale             types.String `tfsdk:"preferred_locale"`
	PremiumProgressBarEnabled   types.Bool   `tfsdk:"premium_progress_bar_enabled"`
	OwnerID                     types.String `tfsdk:"owner_id"`
	Features                    types.Set    `tfsdk:"features"`
	AuditLogReason              types.String `tfsdk:"audit_log_reason"`
}

func newServerSettingsResource() resource.Resource {
	return &serverSettingsResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{serverIdentity("server_id", "id")}}}
}

func (r *serverSettingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_settings"
}

func optionalComputedString(desc string, validators ...validator.String) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		Computed:            true,
		Validators:          validators,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// optionalComputedChannel has no UseStateForUnknown because Discord clears
// the setting when the channel is deleted, which can happen in the same apply.
func optionalComputedChannel(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc + " Omit to leave unmanaged.",
		Optional:            true,
		Computed:            true,
		Validators:          []validator.String{snowflakeValidator()},
	}
}

func (r *serverSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the settings of an existing server (guild). Discord does not allow bots to create " +
			"servers, so this resource adopts a server the bot has been invited to. Settings omitted from configuration are " +
			"left unmanaged. Destroying the resource only removes it from Terraform state.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("Server ID."),
			"server_id":        serverIDAttribute(),
			"name":             optionalComputedString("Server name (2-100 characters).", stringvalidator.LengthBetween(2, 100)),
			"description":      optionalComputedString("Server description. Requires Community."),
			"icon": schema.StringAttribute{
				MarkdownDescription: "Server icon as a data URI, e.g. `\"data:image/png;base64,${filebase64(\"icon.png\")}\"`. " +
					"Stored in state; prefer `icon_wo` on Terraform 1.11 or later. Removing the attribute leaves the current icon in place.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.ConflictsWith(path.MatchRoot("icon_wo"))},
			},
			"icon_wo": writeOnlyImage("Server icon as a data URI.", "icon"),
			"icon_wo_version": writeOnlyVersion("icon",
				"Setting or changing it uploads `icon_wo`; removing it leaves the current icon in place."),
			"icon_hash": schema.StringAttribute{
				MarkdownDescription: "Hash of the current icon. Discord only returns this hash, so a change made outside " +
					"Terraform makes the next plan upload the configured icon again.",
				Computed: true,
			},
			"verification_level": optionalComputedString("Verification level members must meet: "+verificationLevels.doc()+".",
				verificationLevels.validator()),
			"default_message_notifications": optionalComputedString("Default notification setting: "+messageNotifications.doc()+".",
				messageNotifications.validator()),
			"explicit_content_filter": optionalComputedString("Explicit media content filter: "+explicitContentFilter.doc()+".",
				explicitContentFilter.validator()),
			"afk_channel_id": optionalComputedChannel("Voice channel inactive members are moved to."),
			"afk_timeout": schema.Int64Attribute{
				MarkdownDescription: "Seconds of inactivity before a member is moved to the AFK channel: `60`, `300`, `900`, `1800` or `3600`.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.Int64{int64validator.OneOf(60, 300, 900, 1800, 3600)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"system_channel_id": optionalComputedChannel("Channel that receives system messages such as member joins."),
			"system_channel_flags": schema.Int64Attribute{
				MarkdownDescription: "System channel flags bitfield, e.g. `1` suppresses join notifications. See the Discord documentation for values.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.Int64{int64validator.Between(0, 63)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"rules_channel_id":          optionalComputedChannel("Rules channel. Requires Community."),
			"public_updates_channel_id": optionalComputedChannel("Channel that receives notices from Discord. Requires Community."),
			"safety_alerts_channel_id":  optionalComputedChannel("Channel that receives safety alerts. Requires Community."),
			"preferred_locale":          optionalComputedString("Preferred locale, e.g. `en-US`. Requires Community."),
			"premium_progress_bar_enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the boost progress bar is shown.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"owner_id": schema.StringAttribute{
				MarkdownDescription: "ID of the server owner.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"features": schema.SetAttribute{
				MarkdownDescription: "Enabled server features, e.g. `COMMUNITY`.",
				ElementType:         types.StringType,
				Computed:            true,
			},
		},
	}
}

func (r *serverSettingsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

// payload includes only settings that are configured; unset settings stay
// unmanaged.
func (m *serverSettingsModel) payload() discord.Payload {
	p := discord.Payload{}
	putKnownString(p, "name", m.Name)
	putKnownString(p, "description", m.Description)
	verificationLevels.put(p, "verification_level", m.VerificationLevel)
	messageNotifications.put(p, "default_message_notifications", m.DefaultMessageNotifications)
	explicitContentFilter.put(p, "explicit_content_filter", m.ExplicitContentFilter)
	putKnownString(p, "afk_channel_id", m.AFKChannelID)
	putKnownInt(p, "afk_timeout", m.AFKTimeout)
	putKnownString(p, "system_channel_id", m.SystemChannelID)
	putKnownInt(p, "system_channel_flags", m.SystemChannelFlags)
	putKnownString(p, "rules_channel_id", m.RulesChannelID)
	putKnownString(p, "public_updates_channel_id", m.PublicUpdatesChannelID)
	putKnownString(p, "safety_alerts_channel_id", m.SafetyAlertsChannelID)
	putKnownString(p, "preferred_locale", m.PreferredLocale)
	putBool(p, "premium_progress_bar_enabled", m.PremiumProgressBarEnabled)
	putKnownString(p, "icon", m.Icon)
	return p
}

func (m *serverSettingsModel) apply(ctx context.Context, g *discord.Guild, diags *diag.Diagnostics) {
	m.ID = types.StringValue(g.ID)
	m.ServerID = types.StringValue(g.ID)
	m.Name = types.StringValue(g.Name)
	m.Description = stringPtrValue(g.Description)
	m.IconHash = stringPtrValue(g.Icon)
	m.VerificationLevel = verificationLevels.name(g.VerificationLevel)
	m.DefaultMessageNotifications = messageNotifications.name(g.DefaultMessageNotifications)
	m.ExplicitContentFilter = explicitContentFilter.name(g.ExplicitContentFilter)
	m.AFKChannelID = stringPtrValue(g.AFKChannelID)
	m.AFKTimeout = types.Int64Value(g.AFKTimeout)
	m.SystemChannelID = stringPtrValue(g.SystemChannelID)
	m.SystemChannelFlags = types.Int64Value(g.SystemChannelFlags)
	m.RulesChannelID = stringPtrValue(g.RulesChannelID)
	m.PublicUpdatesChannelID = stringPtrValue(g.PublicUpdatesChannelID)
	m.SafetyAlertsChannelID = stringPtrValue(g.SafetyAlertsChannelID)
	m.PreferredLocale = types.StringValue(g.PreferredLocale)
	m.PremiumProgressBarEnabled = types.BoolValue(g.PremiumProgressBarEnabled)
	m.OwnerID = types.StringValue(g.OwnerID)
	m.Features = stringSetValue(ctx, g.Features, diags)
}

func (r *serverSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan serverSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	g, err := r.client.GetGuild(ctx, plan.ServerID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "read server", err)
		return
	}
	var current serverSettingsModel
	current.apply(ctx, g, &resp.Diagnostics)
	p := diffPayload(plan.payload(), current.payload())
	if !plan.IconWOVersion.IsNull() {
		putKnownString(p, "icon", writeOnlyString(ctx, req.Config, "icon_wo", &resp.Diagnostics))
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if len(p) > 0 {
		if g, err = r.client.ModifyGuild(ctx, plan.ServerID.ValueString(), p); err != nil {
			apiError(&resp.Diagnostics, "update server", err)
			return
		}
	}
	plan.apply(ctx, g, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state serverSettingsModel
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
	clearImageOnDrift(state.IconHash, g.Icon, &state.Icon, &state.IconWOVersion)
	state.apply(ctx, g, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serverSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan, state serverSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	p := diffPayload(plan.payload(), state.payload())
	if writeOnlyChanged(plan.IconWOVersion, state.IconWOVersion) {
		putKnownString(p, "icon", writeOnlyString(ctx, req.Config, "icon_wo", &resp.Diagnostics))
	}
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.ModifyGuild(ctx, state.ServerID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "update server", err)
		return
	}
	plan.apply(ctx, g, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverSettingsResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}
