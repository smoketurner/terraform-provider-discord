package provider

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	// eventWebhookStatuses has no 0; Discord's default is disabled.
	eventWebhookStatuses = enumMapping{"", "disabled", "enabled", "disabled_by_discord"}
	// integrationTypes are the keys of integration_types_config.
	integrationTypes = enumMapping{"guild_install", "user_install"}
	// installScopes are the scopes a Discord provided install link can request.
	installScopes = []string{"applications.commands", "bot"}
	urlRegexp     = regexp.MustCompile(`^(https?://\S+)?$`)
)

// limitedIntents maps each limited intent argument to its application flag.
var limitedIntents = []struct {
	name, flagName string
	flag           int64
}{
	{"gateway_presence_limited", "GATEWAY_PRESENCE_LIMITED", discord.ApplicationFlagGatewayPresenceLimited},
	{"gateway_guild_members_limited", "GATEWAY_GUILD_MEMBERS_LIMITED", discord.ApplicationFlagGatewayGuildMembersLimited},
	{"gateway_message_content_limited", "GATEWAY_MESSAGE_CONTENT_LIMITED", discord.ApplicationFlagGatewayMessageContentLimited},
}

var (
	_ resource.ResourceWithConfigure   = &applicationSettingsResource{}
	_ resource.ResourceWithImportState = &applicationSettingsResource{}
	_ resource.ResourceWithIdentity    = &applicationSettingsResource{}
)

type applicationSettingsResource struct {
	resourceIdentity
	client *discord.Client
}

type applicationSettingsModel struct {
	ID                             types.String `tfsdk:"id"`
	Name                           types.String `tfsdk:"name"`
	Description                    types.String `tfsdk:"description"`
	Tags                           types.Set    `tfsdk:"tags"`
	CustomInstallURL               types.String `tfsdk:"custom_install_url"`
	InstallParams                  types.Object `tfsdk:"install_params"`
	IntegrationTypesConfig         types.Map    `tfsdk:"integration_types_config"`
	RoleConnectionsVerificationURL types.String `tfsdk:"role_connections_verification_url"`
	InteractionsEndpointURL        types.String `tfsdk:"interactions_endpoint_url"`
	EventWebhooksURL               types.String `tfsdk:"event_webhooks_url"`
	EventWebhooksStatus            types.String `tfsdk:"event_webhooks_status"`
	EventWebhooksTypes             types.Set    `tfsdk:"event_webhooks_types"`
	GatewayPresenceLimited         types.Bool   `tfsdk:"gateway_presence_limited"`
	GatewayGuildMembersLimited     types.Bool   `tfsdk:"gateway_guild_members_limited"`
	GatewayMessageContentLimited   types.Bool   `tfsdk:"gateway_message_content_limited"`
	Flags                          types.Int64  `tfsdk:"flags"`
	Icon                           types.String `tfsdk:"icon"`
	IconWO                         types.String `tfsdk:"icon_wo"`
	IconWOVersion                  types.Int64  `tfsdk:"icon_wo_version"`
	IconHash                       types.String `tfsdk:"icon_hash"`
	CoverImage                     types.String `tfsdk:"cover_image"`
	CoverImageWO                   types.String `tfsdk:"cover_image_wo"`
	CoverImageWOVersion            types.Int64  `tfsdk:"cover_image_wo_version"`
	CoverImageHash                 types.String `tfsdk:"cover_image_hash"`
}

type installParamsModel struct {
	Scopes      types.Set    `tfsdk:"scopes"`
	Permissions types.String `tfsdk:"permissions"`
}

var installParamsAttrTypes = map[string]attr.Type{
	"scopes":      types.SetType{ElemType: types.StringType},
	"permissions": types.StringType,
}

func newApplicationSettingsResource() resource.Resource {
	return &applicationSettingsResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		{name: "application_id", description: "ID of the application.", state: []string{"id"}},
	}}}
}

func (r *applicationSettingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application_settings"
}

func optionalComputedURL(desc string) schema.StringAttribute {
	return optionalComputedString(desc+clearHint, stringvalidator.UTF8LengthAtMost(2048),
		stringvalidator.RegexMatches(urlRegexp, `must be an http or https URL, or "" to clear the setting`))
}

func installParamsAttributes(required bool) map[string]schema.Attribute {
	scopes := schema.SetAttribute{
		MarkdownDescription: "OAuth2 scopes to add the application with: `applications.commands`, `bot` or both.",
		ElementType:         types.StringType,
		Required:            required,
		Optional:            !required,
		Validators:          []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(stringvalidator.OneOf(installScopes...))},
	}
	permissions := schema.StringAttribute{
		MarkdownDescription: "Permissions to request for the bot role, as a decimal bitfield, e.g. from " +
			"`provider::discord::permissions()`. Only used with the `bot` scope.",
		Required:   required,
		Optional:   !required,
		Validators: []validator.String{permissionsValidator()},
	}
	if !required {
		scopes.MarkdownDescription += " Requires `permissions`."
		scopes.Validators = append(scopes.Validators, setvalidator.AlsoRequires(path.MatchRelative().AtParent().AtName("permissions")))
		permissions.MarkdownDescription += " Requires `scopes`."
		permissions.Validators = append(permissions.Validators, stringvalidator.AlsoRequires(path.MatchRelative().AtParent().AtName("scopes")))
	}
	return map[string]schema.Attribute{"scopes": scopes, "permissions": permissions}
}

func (r *applicationSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id": idAttribute("ID of the application."),
		"name": schema.StringAttribute{
			MarkdownDescription: "Name of the application. Discord does not let bots change it.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"description": optionalComputedString("Description of the application, at most 400 characters. Set to `\"\"` "+
			"to clear it.", stringvalidator.UTF8LengthAtMost(400)),
		"tags": schema.SetAttribute{
			MarkdownDescription: "Tags describing the application's content and functionality: at most 5, of 1-20 " +
				"characters each.",
			ElementType: types.StringType,
			Optional:    true,
			Computed:    true,
			Validators: []validator.Set{
				setvalidator.SizeAtMost(5),
				setvalidator.ValueStringsAre(stringvalidator.UTF8LengthBetween(1, 20)),
			},
			PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
		},
		"custom_install_url": optionalComputedURL("Custom URL of the application's install link, used in place of " +
			"the link Discord provides."),
		"install_params": schema.SingleNestedAttribute{
			MarkdownDescription: "Scopes and permissions of the install link Discord provides. Superseded by " +
				"`integration_types_config` for applications that support user installs." + unmanagedNestedHint,
			Optional:   true,
			Attributes: installParamsAttributes(true),
		},
		"integration_types_config": schema.MapNestedAttribute{
			MarkdownDescription: "The installation contexts the application supports, keyed by " + integrationTypes.doc() +
				", each with the scopes and permissions of its install link. An empty object, `{}`, supports the " +
				"context without an install link." + unmanagedNestedHint,
			Optional: true,
			NestedObject: schema.NestedAttributeObject{
				Attributes: installParamsAttributes(false),
			},
			Validators: []validator.Map{
				mapvalidator.SizeAtLeast(1),
				mapvalidator.KeysAre(integrationTypes.validator()),
			},
		},
		"role_connections_verification_url": optionalComputedURL("URL users visit to link their account for " +
			"linked roles; see `discord_application_role_connection_metadata`."),
		"interactions_endpoint_url": optionalComputedURL("URL that receives interactions over HTTP instead of the " +
			"gateway. Discord sends it a test request when it is saved and rejects the URL unless it responds " +
			"correctly."),
		"event_webhooks_url": optionalComputedURL("URL that receives webhook events."),
		"event_webhooks_status": optionalComputedString("Whether webhook events are sent: `disabled` or `enabled`. "+
			"Discord reports `disabled_by_discord` when it has turned them off, usually after the URL stopped responding.",
			stringvalidator.OneOf("disabled", "enabled")),
		"event_webhooks_types": schema.SetAttribute{
			MarkdownDescription: "Webhook event types to receive, e.g. `APPLICATION_AUTHORIZED`.",
			ElementType:         types.StringType,
			Optional:            true,
			Computed:            true,
			PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
		},
		"flags": schema.Int64Attribute{
			MarkdownDescription: "The application's public flags bitfield. Of these, Discord only lets bots change the " +
				"limited intents, each with its own argument.",
			Computed: true,
		},
	}
	for _, intent := range limitedIntents {
		attrs[intent.name] = schema.BoolAttribute{
			MarkdownDescription: fmt.Sprintf("Whether the `%s` intent, flag `%d`, is enabled. It is the intent for bots "+
				"in fewer than 100 servers; larger bots need Discord's approval. Omit to leave unmanaged.",
				intent.flagName, intent.flag),
			Optional:      true,
			Computed:      true,
			PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
		}
	}
	imageAttributes(attrs, "icon", "Application", "icon", "")
	imageAttributes(attrs, "cover_image", "Application", "cover image", " Shown as the default rich presence invite cover.")
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the settings of the application the provider's bot token belongs to, as on the " +
			"Developer Portal. There is one per bot token. Settings omitted from configuration are left unmanaged; set " +
			"a URL or `description` to `\"\"` to clear it. Destroying the resource only removes it from Terraform state.",
		Attributes: attrs,
	}
}

func (r *applicationSettingsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *applicationSettingsModel) images() []imageArg[discord.Application] {
	return []imageArg[discord.Application]{
		{"icon", &m.Icon, &m.IconWOVersion, &m.IconHash, func(a *discord.Application) *string { return a.Icon }},
		{"cover_image", &m.CoverImage, &m.CoverImageWOVersion, &m.CoverImageHash,
			func(a *discord.Application) *string { return a.CoverImage }},
	}
}

func (m *applicationSettingsModel) intents() []*types.Bool {
	return []*types.Bool{&m.GatewayPresenceLimited, &m.GatewayGuildMembersLimited, &m.GatewayMessageContentLimited}
}

// limitedFlags returns the limited intent flags of base with the configured
// intents applied. Discord accepts only these flags.
func (m *applicationSettingsModel) limitedFlags(base int64) int64 {
	flags := base & discord.ApplicationLimitedIntentFlags
	for i, enabled := range m.intents() {
		switch {
		case !isSet(*enabled):
		case enabled.ValueBool():
			flags |= limitedIntents[i].flag
		default:
			flags &^= limitedIntents[i].flag
		}
	}
	return flags
}

func installParamsPayload(ctx context.Context, m installParamsModel, diags *diag.Diagnostics) map[string]any {
	scopes := []string{}
	diags.Append(m.Scopes.ElementsAs(ctx, &scopes, false)...)
	return map[string]any{"scopes": scopes, "permissions": m.Permissions.ValueString()}
}

// payload includes only settings that are configured; unset settings stay
// unmanaged. base is the application's current flags.
func (m *applicationSettingsModel) payload(ctx context.Context, base int64, diags *diag.Diagnostics) discord.Payload {
	p := discord.Payload{}
	putKnownString(p, "description", m.Description)
	if isSet(m.Tags) {
		tags := []string{}
		diags.Append(m.Tags.ElementsAs(ctx, &tags, false)...)
		p["tags"] = tags
	}
	putClearable(p, "custom_install_url", m.CustomInstallURL)
	if isSet(m.InstallParams) {
		var ip installParamsModel
		diags.Append(m.InstallParams.As(ctx, &ip, basetypes.ObjectAsOptions{})...)
		p["install_params"] = installParamsPayload(ctx, ip, diags)
	}
	if isSet(m.IntegrationTypesConfig) {
		configs := map[string]installParamsModel{}
		diags.Append(m.IntegrationTypesConfig.ElementsAs(ctx, &configs, false)...)
		out := map[string]any{}
		for name, c := range configs {
			v, _ := integrationTypes.value(name)
			cfg := map[string]any{}
			if isSet(c.Scopes) {
				cfg["oauth2_install_params"] = installParamsPayload(ctx, c, diags)
			}
			out[strconv.FormatInt(v, 10)] = cfg
		}
		p["integration_types_config"] = out
	}
	putClearable(p, "role_connections_verification_url", m.RoleConnectionsVerificationURL)
	putClearable(p, "interactions_endpoint_url", m.InteractionsEndpointURL)
	putClearable(p, "event_webhooks_url", m.EventWebhooksURL)
	eventWebhookStatuses.put(p, "event_webhooks_status", m.EventWebhooksStatus)
	if isSet(m.EventWebhooksTypes) {
		eventTypes := []string{}
		diags.Append(m.EventWebhooksTypes.ElementsAs(ctx, &eventTypes, false)...)
		p["event_webhooks_types"] = eventTypes
	}
	p["flags"] = m.limitedFlags(base)
	for _, img := range m.images() {
		putKnownString(p, img.name, *img.image)
	}
	return p
}

// installParamsValue converts install params, whose scopes and permissions
// are null when ip is nil.
func installParamsValue(ctx context.Context, ip *discord.InstallParams, diags *diag.Diagnostics) types.Object {
	v := installParamsModel{Scopes: types.SetNull(types.StringType), Permissions: types.StringNull()}
	if ip != nil {
		v = installParamsModel{Scopes: stringSetValue(ctx, ip.Scopes, diags), Permissions: types.StringValue(ip.Permissions)}
	}
	obj, d := types.ObjectValueFrom(ctx, installParamsAttrTypes, v)
	diags.Append(d...)
	return obj
}

// unmanagedNestedHint documents the nested settings that are managed only
// while configured. An optional and computed nested attribute would show a
// change in every plan where it is not configured.
const unmanagedNestedHint = " Managed only while set; removing it leaves the current setting in place."

func (m *applicationSettingsModel) apply(ctx context.Context, a *discord.Application, diags *diag.Diagnostics) {
	m.ID = types.StringValue(a.ID)
	m.Name = types.StringValue(a.Name)
	m.Description = types.StringValue(a.Description)
	m.Tags = stringSetValue(ctx, a.Tags, diags)
	m.CustomInstallURL = clearableValue(m.CustomInstallURL, a.CustomInstallURL)
	if !m.InstallParams.IsNull() {
		m.InstallParams = types.ObjectNull(installParamsAttrTypes)
		if a.InstallParams != nil {
			m.InstallParams = installParamsValue(ctx, a.InstallParams, diags)
		}
	}
	objType := types.ObjectType{AttrTypes: installParamsAttrTypes}
	switch {
	case m.IntegrationTypesConfig.IsNull():
	case a.IntegrationTypesConfig == nil:
		m.IntegrationTypesConfig = types.MapNull(objType)
	default:
		configs := map[string]attr.Value{}
		for key, c := range a.IntegrationTypesConfig {
			v, _ := strconv.ParseInt(key, 10, 64)
			configs[integrationTypes.name(v).ValueString()] = installParamsValue(ctx, c.OAuth2InstallParams, diags)
		}
		var d diag.Diagnostics
		m.IntegrationTypesConfig, d = types.MapValue(objType, configs)
		diags.Append(d...)
	}
	m.RoleConnectionsVerificationURL = clearableValue(m.RoleConnectionsVerificationURL, a.RoleConnectionsVerificationURL)
	m.InteractionsEndpointURL = clearableValue(m.InteractionsEndpointURL, a.InteractionsEndpointURL)
	m.EventWebhooksURL = clearableValue(m.EventWebhooksURL, a.EventWebhooksURL)
	status := a.EventWebhooksStatus
	if status == 0 {
		status = discord.EventWebhooksDisabled
	}
	m.EventWebhooksStatus = eventWebhookStatuses.name(status)
	m.EventWebhooksTypes = stringSetValue(ctx, a.EventWebhooksTypes, diags)
	for i, enabled := range m.intents() {
		*enabled = types.BoolValue(a.Flags&limitedIntents[i].flag != 0)
	}
	m.Flags = types.Int64Value(a.Flags)
	setImageHashes(m.images(), a)
}

func (r *applicationSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan applicationSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	a, err := r.client.GetCurrentApplication(ctx)
	if err != nil {
		apiError(&resp.Diagnostics, "read application", err)
		return
	}
	var current applicationSettingsModel
	current.apply(ctx, a, &resp.Diagnostics)
	p := diffPayload(plan.payload(ctx, a.Flags, &resp.Diagnostics), current.payload(ctx, a.Flags, &resp.Diagnostics))
	putWriteOnlyImages(ctx, req.Config, p, plan.images(), nil, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(p) > 0 {
		if a, err = r.client.ModifyCurrentApplication(ctx, p); err != nil {
			apiError(&resp.Diagnostics, "update application", err)
			return
		}
	}
	plan.apply(ctx, a, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *applicationSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state applicationSettingsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	a, err := r.client.GetCurrentApplication(ctx)
	if err != nil {
		apiError(&resp.Diagnostics, "read application", err)
		return
	}
	if state.ID.ValueString() != a.ID {
		resp.Diagnostics.AddError("Wrong application", fmt.Sprintf(
			"The provider's token belongs to application %s, not %s. A bot can only manage its own application.",
			a.ID, state.ID.ValueString()))
		return
	}
	clearImagesOnDrift(state.images(), a)
	state.apply(ctx, a, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *applicationSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan, state applicationSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	base := state.Flags.ValueInt64()
	p := diffPayload(plan.payload(ctx, base, &resp.Diagnostics), state.payload(ctx, base, &resp.Diagnostics))
	putWriteOnlyImages(ctx, req.Config, p, plan.images(), state.images(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	var a *discord.Application
	var err error
	if len(p) == 0 {
		a, err = r.client.GetCurrentApplication(ctx)
	} else {
		a, err = r.client.ModifyCurrentApplication(ctx, p)
	}
	if err != nil {
		apiError(&resp.Diagnostics, "update application", err)
		return
	}
	plan.apply(ctx, a, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete leaves the application's settings as they are.
func (r *applicationSettingsResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}
