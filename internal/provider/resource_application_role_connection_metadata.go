package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	roleConnectionMetadataTypes = enumMapping{
		"",
		"integer_less_than_or_equal", "integer_greater_than_or_equal", "integer_equal", "integer_not_equal",
		"datetime_less_than_or_equal", "datetime_greater_than_or_equal", "boolean_equal", "boolean_not_equal",
	}
	roleConnectionKeyRegexp = regexp.MustCompile(`^[a-z0-9_]{1,50}$`)
)

// maxRoleConnectionMetadata is how many metadata records an application can
// have.
const maxRoleConnectionMetadata = 5

var (
	_ resource.ResourceWithConfigure   = &roleConnectionMetadataResource{}
	_ resource.ResourceWithImportState = &roleConnectionMetadataResource{}
	_ resource.ResourceWithIdentity    = &roleConnectionMetadataResource{}
)

type roleConnectionMetadataResource struct {
	resourceIdentity
	client *discord.Client
}

type roleConnectionMetadataModel struct {
	ID            types.String                   `tfsdk:"id"`
	ApplicationID types.String                   `tfsdk:"application_id"`
	Records       []roleConnectionMetadataRecord `tfsdk:"records"`
}

type roleConnectionMetadataRecord struct {
	Type                     types.String `tfsdk:"type"`
	Key                      types.String `tfsdk:"key"`
	Name                     types.String `tfsdk:"name"`
	NameLocalizations        types.Map    `tfsdk:"name_localizations"`
	Description              types.String `tfsdk:"description"`
	DescriptionLocalizations types.Map    `tfsdk:"description_localizations"`
}

func newRoleConnectionMetadataResource() resource.Resource {
	return &roleConnectionMetadataResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		applicationIdentity("application_id", "id"),
	}}}
}

// resourceApplicationIDAttribute is the application a resource belongs to. A bot
// token can only manage its own application, which is the default.
func resourceApplicationIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "ID of the application. Defaults to the application the provider's bot token belongs to, " +
			"the only one it can manage.",
		Optional:   true,
		Computed:   true,
		Validators: []validator.String{snowflakeValidator()},
		PlanModifiers: []planmodifier.String{
			stringplanmodifier.UseStateForUnknown(),
			stringplanmodifier.RequiresReplace(),
		},
	}
}

func applicationIdentity(state ...string) identityAttribute {
	return identityAttribute{name: "application_id", description: "ID of the application.", state: state}
}

// applicationID is resolveApplicationID for a planned value, which is unknown
// when application_id is not configured.
func applicationID(ctx context.Context, c *discord.Client, v types.String, diags *diag.Diagnostics) string {
	if v.IsUnknown() {
		v = types.StringNull()
	}
	id, _ := resolveApplicationID(ctx, c, v, diags)
	return id.ValueString()
}

func (r *roleConnectionMetadataResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application_role_connection_metadata"
}

func (r *roleConnectionMetadataResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the role connection metadata of an application: the requirements servers can " +
			"set on a linked role that uses the application, such as \"has been a member for at least N days\". The " +
			"application sets each user's values with an OAuth2 token, and users link their account at the " +
			"application's `role_connections_verification_url` (see `discord_application_settings`).\n\n" +
			"Discord replaces the whole list at once, so this resource is authoritative: creating it replaces any " +
			"existing records, and destroying it removes them all. Discord's API cannot attach these requirements " +
			"to a role; that remains a manual step in the server's role settings.",
		Attributes: map[string]schema.Attribute{
			"id":             idAttribute("ID of the application."),
			"application_id": resourceApplicationIDAttribute(),
			"records": schema.ListNestedAttribute{
				MarkdownDescription: "The metadata records, at most 5, each with a unique `key`. An empty list " +
					"removes them all.",
				Required:   true,
				Validators: []validator.List{listvalidator.SizeAtMost(maxRoleConnectionMetadata), uniqueRecordKeys{}},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						MarkdownDescription: "How a user's value is compared with the value a server sets: " +
							roleConnectionMetadataTypes.doc() + ". Integer and boolean values are integers; datetime " +
							"values are ISO8601 timestamps compared with a number of days before the current date.",
						Required:   true,
						Validators: []validator.String{roleConnectionMetadataTypes.validator()},
					},
					"key": schema.StringAttribute{
						MarkdownDescription: "Key of the value in users' role connection metadata: 1-50 characters " +
							"of `a-z`, `0-9` and `_`.",
						Required: true,
						Validators: []validator.String{stringvalidator.RegexMatches(roleConnectionKeyRegexp,
							"must be 1-50 characters of a-z, 0-9 and _")},
					},
					"name": schema.StringAttribute{
						MarkdownDescription: "Name shown to servers (1-100 characters).",
						Required:            true,
						Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 100)},
					},
					"name_localizations": localizationsAttribute("name", 100),
					"description": schema.StringAttribute{
						MarkdownDescription: "Description shown to servers (1-200 characters).",
						Required:            true,
						Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 200)},
					},
					"description_localizations": localizationsAttribute("description", 200),
				}},
			},
		},
	}
}

// uniqueRecordKeys rejects metadata records that share a key.
type uniqueRecordKeys struct{}

func (uniqueRecordKeys) Description(context.Context) string {
	return "each record must have a unique key"
}

func (v uniqueRecordKeys) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (uniqueRecordKeys) ValidateList(ctx context.Context, req validator.ListRequest, resp *validator.ListResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var records []roleConnectionMetadataRecord
	if req.ConfigValue.ElementsAs(ctx, &records, false).HasError() {
		return
	}
	seen := map[string]bool{}
	for i, rec := range records {
		if !isSet(rec.Key) {
			continue
		}
		if key := rec.Key.ValueString(); seen[key] {
			resp.Diagnostics.AddAttributeError(req.Path.AtListIndex(i).AtName("key"), "Duplicate key",
				"Each role connection metadata record must have a unique key; "+key+" is used more than once.")
		}
		seen[rec.Key.ValueString()] = true
	}
}

func (r *roleConnectionMetadataResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func localizations(ctx context.Context, v types.Map, diags *diag.Diagnostics) map[string]string {
	if v.IsNull() {
		return nil
	}
	out := map[string]string{}
	diags.Append(v.ElementsAs(ctx, &out, false)...)
	return out
}

func localizationsValue(ctx context.Context, m map[string]string, diags *diag.Diagnostics) types.Map {
	if len(m) == 0 {
		return types.MapNull(types.StringType)
	}
	v, d := types.MapValueFrom(ctx, types.StringType, m)
	diags.Append(d...)
	return v
}

func (m *roleConnectionMetadataModel) records(ctx context.Context, diags *diag.Diagnostics) []discord.RoleConnectionMetadata {
	out := make([]discord.RoleConnectionMetadata, 0, len(m.Records))
	for _, rec := range m.Records {
		typ, _ := roleConnectionMetadataTypes.value(rec.Type.ValueString())
		out = append(out, discord.RoleConnectionMetadata{
			Type:                     typ,
			Key:                      rec.Key.ValueString(),
			Name:                     rec.Name.ValueString(),
			NameLocalizations:        localizations(ctx, rec.NameLocalizations, diags),
			Description:              rec.Description.ValueString(),
			DescriptionLocalizations: localizations(ctx, rec.DescriptionLocalizations, diags),
		})
	}
	return out
}

func (m *roleConnectionMetadataModel) apply(ctx context.Context, applicationID string, records []discord.RoleConnectionMetadata, diags *diag.Diagnostics) {
	m.ID = types.StringValue(applicationID)
	m.ApplicationID = types.StringValue(applicationID)
	m.Records = make([]roleConnectionMetadataRecord, 0, len(records))
	for _, rec := range records {
		m.Records = append(m.Records, roleConnectionMetadataRecord{
			Type:                     roleConnectionMetadataTypes.name(rec.Type),
			Key:                      types.StringValue(rec.Key),
			Name:                     types.StringValue(rec.Name),
			NameLocalizations:        localizationsValue(ctx, rec.NameLocalizations, diags),
			Description:              types.StringValue(rec.Description),
			DescriptionLocalizations: localizationsValue(ctx, rec.DescriptionLocalizations, diags),
		})
	}
}

// put replaces the application's records with the planned ones.
func (r *roleConnectionMetadataResource) put(ctx context.Context, plan *roleConnectionMetadataModel, diags *diag.Diagnostics) {
	id := applicationID(ctx, r.client, plan.ApplicationID, diags)
	records := plan.records(ctx, diags)
	if diags.HasError() {
		return
	}
	out, err := r.client.UpdateRoleConnectionMetadata(ctx, id, records)
	if err != nil {
		apiError(diags, "update role connection metadata", err)
		return
	}
	plan.apply(ctx, id, out, diags)
}

func (r *roleConnectionMetadataResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan roleConnectionMetadataModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.put(ctx, &plan, &resp.Diagnostics); resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *roleConnectionMetadataResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state roleConnectionMetadataModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ApplicationID.ValueString()
	records, err := r.client.GetRoleConnectionMetadata(ctx, id)
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read role connection metadata", err)
		return
	}
	state.apply(ctx, id, records, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *roleConnectionMetadataResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan roleConnectionMetadataModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.put(ctx, &plan, &resp.Diagnostics); resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes every record, as Discord only replaces the whole list.
func (r *roleConnectionMetadataResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state roleConnectionMetadataModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.UpdateRoleConnectionMetadata(ctx, state.ApplicationID.ValueString(), []discord.RoleConnectionMetadata{})
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "remove role connection metadata", err)
	}
}
