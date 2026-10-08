package provider

import (
	"context"
	"regexp"
	"slices"

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
	_ resource.ResourceWithConfigure      = &serverSettingsResource{}
	_ resource.ResourceWithImportState    = &serverSettingsResource{}
	_ resource.ResourceWithIdentity       = &serverSettingsResource{}
	_ resource.ResourceWithValidateConfig = &serverSettingsResource{}
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
	Banner                      types.String `tfsdk:"banner"`
	BannerWO                    types.String `tfsdk:"banner_wo"`
	BannerWOVersion             types.Int64  `tfsdk:"banner_wo_version"`
	BannerHash                  types.String `tfsdk:"banner_hash"`
	Splash                      types.String `tfsdk:"splash"`
	SplashWO                    types.String `tfsdk:"splash_wo"`
	SplashWOVersion             types.Int64  `tfsdk:"splash_wo_version"`
	SplashHash                  types.String `tfsdk:"splash_hash"`
	DiscoverySplash             types.String `tfsdk:"discovery_splash"`
	DiscoverySplashWO           types.String `tfsdk:"discovery_splash_wo"`
	DiscoverySplashWOVersion    types.Int64  `tfsdk:"discovery_splash_wo_version"`
	DiscoverySplashHash         types.String `tfsdk:"discovery_splash_hash"`
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
	Community                   types.Bool   `tfsdk:"community"`
	Discoverable                types.Bool   `tfsdk:"discoverable"`
	InvitesDisabled             types.Bool   `tfsdk:"invites_disabled"`
	RaidAlertsDisabled          types.Bool   `tfsdk:"raid_alerts_disabled"`
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

// clearHint documents the empty string that clears a nullable setting. An
// omitted optional and computed argument cannot be told apart from null, so
// omitting a setting leaves it unmanaged instead.
const clearHint = " Set to `\"\"` to clear the setting; omit to leave it unmanaged."

var optionalSnowflakeRegexp = regexp.MustCompile(`^([0-9]{1,20})?$`)

// optionalComputedChannel has no UseStateForUnknown because Discord clears
// the setting when the channel is deleted, which can happen in the same apply.
func optionalComputedChannel(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc + clearHint,
		Optional:            true,
		Computed:            true,
		Validators: []validator.String{
			stringvalidator.RegexMatches(optionalSnowflakeRegexp, `must be a Discord snowflake ID, or "" to clear the setting`),
		},
	}
}

// featureFlag is an optional mutable server feature. It has no
// UseStateForUnknown because Discord may change other features along with
// the one requested, such as when Community is disabled.
func featureFlag(desc string) schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: desc + " Omit to leave unmanaged.",
		Optional:            true,
		Computed:            true,
	}
}

// serverImageAttributes returns the arguments of an uploaded server image,
// following the write-only convention in write_only.go.
func serverImageAttributes(attrs map[string]schema.Attribute, name, label, requirement string) {
	attrs[name] = schema.StringAttribute{
		MarkdownDescription: "Server " + label + " as a data URI, e.g. `\"data:image/png;base64,${filebase64(\"" + name +
			".png\")}\"`." + requirement + " Stored in state; prefer `" + name + "_wo` on Terraform 1.11 or later. " +
			"Removing the attribute leaves the current " + label + " in place.",
		Optional:   true,
		Validators: []validator.String{dataURIValidator(), stringvalidator.ConflictsWith(path.MatchRoot(name + "_wo"))},
	}
	attrs[name+"_wo"] = writeOnlyImage("Server "+label+" as a data URI."+requirement, name)
	attrs[name+"_wo_version"] = writeOnlyVersion(name,
		"Setting or changing it uploads `"+name+"_wo`; removing it leaves the current "+label+" in place.")
	attrs[name+"_hash"] = schema.StringAttribute{
		MarkdownDescription: "Hash of the current " + label + ". Discord only returns this hash, so a change made outside " +
			"Terraform makes the next plan upload the configured " + label + " again.",
		Computed: true,
	}
}

func (r *serverSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"audit_log_reason": auditLogReasonAttribute(),
		"id":               idAttribute("Server ID."),
		"server_id":        serverIDAttribute(),
		"name":             optionalComputedString("Server name (2-100 characters).", stringvalidator.LengthBetween(2, 100)),
		"description":      optionalComputedString("Server description. Requires Community." + clearHint),
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
		"rules_channel_id":          optionalComputedChannel("Rules channel. Required by Community."),
		"public_updates_channel_id": optionalComputedChannel("Channel that receives notices from Discord. Required by Community."),
		"safety_alerts_channel_id":  optionalComputedChannel("Channel that receives safety alerts. Requires Community."),
		"preferred_locale":          optionalComputedString("Preferred locale, e.g. `en-US`. Requires Community."),
		"premium_progress_bar_enabled": schema.BoolAttribute{
			MarkdownDescription: "Whether the boost progress bar is shown.",
			Optional:            true,
			Computed:            true,
			PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
		},
		"community": featureFlag("Whether Community is enabled (the `COMMUNITY` feature). Changing it requires the " +
			"Administrator permission, and enabling it requires `rules_channel_id` and `public_updates_channel_id`."),
		"discoverable": featureFlag("Whether the server is listed in Server Discovery (the `DISCOVERABLE` feature). " +
			"Changing it requires the Administrator permission, and the server must meet the discovery requirements."),
		"invites_disabled": featureFlag("Whether invites are paused (the `INVITES_DISABLED` feature), preventing new " +
			"members from joining until set back to `false`. For a pause that ends on its own, use " +
			"`discord_server_incident_actions`."),
		"raid_alerts_disabled": featureFlag("Whether join raid alerts in the safety alerts channel are disabled (the " +
			"`RAID_ALERTS_DISABLED` feature)."),
		"owner_id": schema.StringAttribute{
			MarkdownDescription: "ID of the server owner.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"features": schema.SetAttribute{
			MarkdownDescription: "All enabled server features, including those Discord grants, e.g. `NEWS`.",
			ElementType:         types.StringType,
			Computed:            true,
		},
	}
	serverImageAttributes(attrs, "icon", "icon", "")
	serverImageAttributes(attrs, "banner", "banner",
		" Requires the `BANNER` feature; animated GIFs require `ANIMATED_BANNER`.")
	serverImageAttributes(attrs, "splash", "invite splash",
		" Requires the `INVITE_SPLASH` feature.")
	serverImageAttributes(attrs, "discovery_splash", "discovery splash",
		" Requires the `DISCOVERABLE` feature.")
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the settings of an existing server (guild). Discord does not allow bots to create " +
			"servers, so this resource adopts a server the bot has been invited to. Settings omitted from configuration are " +
			"left unmanaged; set a channel setting or `description` to `\"\"` to clear it. Of the server features, only " +
			"the four Discord lets servers change are managed, each with its own argument. Destroying the resource only " +
			"removes it from Terraform state.",
		Attributes: attrs,
	}
}

func (r *serverSettingsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (r *serverSettingsResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m serverSettingsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !isTrue(m.Community) {
		return
	}
	for name, v := range map[string]types.String{"rules_channel_id": m.RulesChannelID, "public_updates_channel_id": m.PublicUpdatesChannelID} {
		if v.IsNull() || (!v.IsUnknown() && v.ValueString() == "") {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Invalid argument",
				name+" must be set to a channel when community is true: Discord requires a rules channel and a public updates channel for Community.")
		}
	}
}

// serverImage ties an uploaded image's arguments to the hash Discord returns.
type serverImage struct {
	name    string
	image   *types.String
	version *types.Int64
	hash    *types.String
	current func(*discord.Guild) *string
}

func (m *serverSettingsModel) images() []serverImage {
	return []serverImage{
		{"icon", &m.Icon, &m.IconWOVersion, &m.IconHash, func(g *discord.Guild) *string { return g.Icon }},
		{"banner", &m.Banner, &m.BannerWOVersion, &m.BannerHash, func(g *discord.Guild) *string { return g.Banner }},
		{"splash", &m.Splash, &m.SplashWOVersion, &m.SplashHash, func(g *discord.Guild) *string { return g.Splash }},
		{"discovery_splash", &m.DiscoverySplash, &m.DiscoverySplashWOVersion, &m.DiscoverySplashHash,
			func(g *discord.Guild) *string { return g.DiscoverySplash }},
	}
}

type featureSetting struct {
	name    string
	enabled *types.Bool
}

func (m *serverSettingsModel) mutableFeatures() []featureSetting {
	return []featureSetting{
		{"COMMUNITY", &m.Community},
		{"DISCOVERABLE", &m.Discoverable},
		{"INVITES_DISABLED", &m.InvitesDisabled},
		{"RAID_ALERTS_DISABLED", &m.RaidAlertsDisabled},
	}
}

// features returns current with the configured mutable features added or
// removed, and whether that changed anything. Modify Guild takes the whole
// list, so the features Discord grants are sent back unchanged.
func (m *serverSettingsModel) features(current []string) ([]string, bool) {
	out := slices.Clone(current)
	changed := false
	for _, f := range m.mutableFeatures() {
		if f.enabled.IsNull() || f.enabled.IsUnknown() {
			continue
		}
		has := slices.Contains(out, f.name)
		switch want := f.enabled.ValueBool(); {
		case want && !has:
			out = append(out, f.name)
			changed = true
		case !want && has:
			out = slices.DeleteFunc(out, func(s string) bool { return s == f.name })
			changed = true
		}
	}
	return out, changed
}

// featuresChanged reports whether a configured feature differs from prior.
func (m *serverSettingsModel) featuresChanged(prior *serverSettingsModel) bool {
	before := prior.mutableFeatures()
	for i, f := range m.mutableFeatures() {
		if isSet(*f.enabled) && !f.enabled.Equal(*before[i].enabled) {
			return true
		}
	}
	return false
}

// putClearable adds a nullable setting when set, sending "" as null so
// Discord clears it.
func putClearable(p discord.Payload, key string, v types.String) {
	switch {
	case v.IsUnknown() || v.IsNull():
	case v.ValueString() == "":
		p[key] = nil
	default:
		p[key] = v.ValueString()
	}
}

// clearableValue keeps a configured "" while Discord reports the setting as
// cleared, so the empty string means "no value" rather than a diff.
func clearableValue(prior types.String, current *string) types.String {
	v := stringPtrValue(current)
	if v.IsNull() && !prior.IsNull() && !prior.IsUnknown() && prior.ValueString() == "" {
		return prior
	}
	return v
}

// payload includes only settings that are configured; unset settings stay
// unmanaged.
func (m *serverSettingsModel) payload() discord.Payload {
	p := discord.Payload{}
	putKnownString(p, "name", m.Name)
	putClearable(p, "description", m.Description)
	verificationLevels.put(p, "verification_level", m.VerificationLevel)
	messageNotifications.put(p, "default_message_notifications", m.DefaultMessageNotifications)
	explicitContentFilter.put(p, "explicit_content_filter", m.ExplicitContentFilter)
	putClearable(p, "afk_channel_id", m.AFKChannelID)
	putKnownInt(p, "afk_timeout", m.AFKTimeout)
	putClearable(p, "system_channel_id", m.SystemChannelID)
	putKnownInt(p, "system_channel_flags", m.SystemChannelFlags)
	putClearable(p, "rules_channel_id", m.RulesChannelID)
	putClearable(p, "public_updates_channel_id", m.PublicUpdatesChannelID)
	putClearable(p, "safety_alerts_channel_id", m.SafetyAlertsChannelID)
	putKnownString(p, "preferred_locale", m.PreferredLocale)
	putBool(p, "premium_progress_bar_enabled", m.PremiumProgressBarEnabled)
	for _, img := range m.images() {
		putKnownString(p, img.name, *img.image)
	}
	return p
}

func (m *serverSettingsModel) apply(ctx context.Context, g *discord.Guild, diags *diag.Diagnostics) {
	m.ID = types.StringValue(g.ID)
	m.ServerID = types.StringValue(g.ID)
	m.Name = types.StringValue(g.Name)
	m.Description = clearableValue(m.Description, g.Description)
	for _, img := range m.images() {
		*img.hash = stringPtrValue(img.current(g))
	}
	m.VerificationLevel = verificationLevels.name(g.VerificationLevel)
	m.DefaultMessageNotifications = messageNotifications.name(g.DefaultMessageNotifications)
	m.ExplicitContentFilter = explicitContentFilter.name(g.ExplicitContentFilter)
	m.AFKChannelID = clearableValue(m.AFKChannelID, g.AFKChannelID)
	m.AFKTimeout = types.Int64Value(g.AFKTimeout)
	m.SystemChannelID = clearableValue(m.SystemChannelID, g.SystemChannelID)
	m.SystemChannelFlags = types.Int64Value(g.SystemChannelFlags)
	m.RulesChannelID = clearableValue(m.RulesChannelID, g.RulesChannelID)
	m.PublicUpdatesChannelID = clearableValue(m.PublicUpdatesChannelID, g.PublicUpdatesChannelID)
	m.SafetyAlertsChannelID = clearableValue(m.SafetyAlertsChannelID, g.SafetyAlertsChannelID)
	m.PreferredLocale = types.StringValue(g.PreferredLocale)
	m.PremiumProgressBarEnabled = types.BoolValue(g.PremiumProgressBarEnabled)
	for _, f := range m.mutableFeatures() {
		*f.enabled = types.BoolValue(slices.Contains(g.Features, f.name))
	}
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
	for _, img := range plan.images() {
		if !img.version.IsNull() {
			putKnownString(p, img.name, writeOnlyString(ctx, req.Config, img.name+"_wo", &resp.Diagnostics))
		}
	}
	if features, changed := plan.features(g.Features); changed {
		p["features"] = features
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
	for _, img := range state.images() {
		clearImageOnDrift(*img.hash, img.current(g), img.image, img.version)
	}
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
	prior := state.images()
	for i, img := range plan.images() {
		if writeOnlyChanged(*img.version, *prior[i].version) {
			putKnownString(p, img.name, writeOnlyString(ctx, req.Config, img.name+"_wo", &resp.Diagnostics))
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.featuresChanged(&state) {
		// Features are sent as a whole list, so start from the server's
		// current list rather than the one refreshed at plan time.
		g, err := r.client.GetGuild(ctx, state.ServerID.ValueString())
		if err != nil {
			apiError(&resp.Diagnostics, "read server", err)
			return
		}
		if features, changed := plan.features(g.Features); changed {
			p["features"] = features
		}
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
