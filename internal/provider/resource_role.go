package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &roleResource{}
	_ resource.ResourceWithImportState = &roleResource{}
)

type roleResource struct {
	client *discord.Client
}

type roleModel struct {
	ID             types.String `tfsdk:"id"`
	ServerID       types.String `tfsdk:"server_id"`
	Name           types.String `tfsdk:"name"`
	Permissions    types.String `tfsdk:"permissions"`
	Color          types.Int64  `tfsdk:"color"`
	SecondaryColor types.Int64  `tfsdk:"secondary_color"`
	TertiaryColor  types.Int64  `tfsdk:"tertiary_color"`
	Hoist          types.Bool   `tfsdk:"hoist"`
	Mentionable    types.Bool   `tfsdk:"mentionable"`
	UnicodeEmoji   types.String `tfsdk:"unicode_emoji"`
	Position       types.Int64  `tfsdk:"position"`
	Managed        types.Bool   `tfsdk:"managed"`
	AuditLogReason types.String `tfsdk:"audit_log_reason"`
}

func newRoleResource() resource.Resource { return &roleResource{} }

func (r *roleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (r *roleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a server role. Role ordering is managed separately with `discord_role_positions` " +
			"so that several roles can be reordered atomically.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("Role ID."),
			"server_id":        serverIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Role name.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 100)},
			},
			"permissions": schema.StringAttribute{
				MarkdownDescription: "Permission bitfield as a decimal string. Use `provider::discord::permissions([...])` to build it. Defaults to `0` (no permissions).",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("0"),
				Validators:          []validator.String{permissionsValidator()},
			},
			"color": schema.Int64Attribute{
				MarkdownDescription: "RGB color as an integer. Use `provider::discord::color(\"#rrggbb\")` to convert a hex color. Defaults to `0` (no color).",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
				Validators:          []validator.Int64{int64validator.Between(0, 0xFFFFFF)},
			},
			"secondary_color": schema.Int64Attribute{
				MarkdownDescription: "Secondary RGB color, which turns the role color into a gradient. Requires the server to have the `ENHANCED_ROLE_COLORS` feature.",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.Between(0, 0xFFFFFF)},
			},
			"tertiary_color": schema.Int64Attribute{
				MarkdownDescription: "Tertiary RGB color for the holographic role style. Requires `ENHANCED_ROLE_COLORS`; Discord only accepts its fixed holographic values (`11127295`, `16759788`, `16761760` as primary, secondary and tertiary).",
				Optional:            true,
				Validators: []validator.Int64{
					int64validator.Between(0, 0xFFFFFF),
					int64validator.AlsoRequires(path.MatchRoot("secondary_color")),
				},
			},
			"hoist": schema.BoolAttribute{
				MarkdownDescription: "Whether members with this role are displayed separately in the member list. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"mentionable": schema.BoolAttribute{
				MarkdownDescription: "Whether anyone can mention this role. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"unicode_emoji": schema.StringAttribute{
				MarkdownDescription: "Unicode emoji used as the role icon. Requires the server to have the `ROLE_ICONS` feature.",
				Optional:            true,
			},
			// No UseStateForUnknown: creating other roles in the same apply
			// shifts this role's position.
			"position": schema.Int64Attribute{
				MarkdownDescription: "Current position of the role. Read-only; use `discord_role_positions` to reorder roles.",
				Computed:            true,
			},
			"managed": schema.BoolAttribute{
				MarkdownDescription: "Whether the role is managed by an integration.",
				Computed:            true,
			},
		},
	}
}

func (r *roleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *roleModel) payload() discord.Payload {
	p := discord.Payload{}
	putString(p, "name", m.Name)
	putString(p, "permissions", m.Permissions)
	if !m.Color.IsUnknown() {
		p["colors"] = discord.RoleColors{
			PrimaryColor:   m.Color.ValueInt64(),
			SecondaryColor: m.SecondaryColor.ValueInt64Pointer(),
			TertiaryColor:  m.TertiaryColor.ValueInt64Pointer(),
		}
	}
	putBool(p, "hoist", m.Hoist)
	putBool(p, "mentionable", m.Mentionable)
	putString(p, "unicode_emoji", m.UnicodeEmoji)
	return p
}

func (m *roleModel) apply(role *discord.Role) {
	m.ID = types.StringValue(role.ID)
	m.Name = types.StringValue(role.Name)
	m.Permissions = types.StringValue(role.Permissions)
	colors := discord.RoleColors{PrimaryColor: role.Color}
	if role.Colors != nil {
		colors = *role.Colors
	}
	m.Color = types.Int64Value(colors.PrimaryColor)
	m.SecondaryColor = types.Int64PointerValue(colors.SecondaryColor)
	m.TertiaryColor = types.Int64PointerValue(colors.TertiaryColor)
	m.Hoist = types.BoolValue(role.Hoist)
	m.Mentionable = types.BoolValue(role.Mentionable)
	m.UnicodeEmoji = stringPtrValue(role.UnicodeEmoji)
	m.Position = types.Int64Value(role.Position)
	m.Managed = types.BoolValue(role.Managed)
}

func (r *roleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan roleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	p := plan.payload()
	if plan.UnicodeEmoji.IsNull() {
		delete(p, "unicode_emoji")
	}
	role, err := r.client.CreateRole(ctx, plan.ServerID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "create role", err)
		return
	}
	plan.apply(role)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *roleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state roleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	role, err := r.client.GetRole(ctx, state.ServerID.ValueString(), state.ID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read role", err)
		return
	}
	state.apply(role)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *roleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan, state roleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	role, err := r.client.ModifyRole(ctx, state.ServerID.ValueString(), state.ID.ValueString(), diffPayload(plan.payload(), state.payload()))
	if err != nil {
		apiError(&resp.Diagnostics, "update role", err)
		return
	}
	plan.apply(role)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *roleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state roleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	err := r.client.DeleteRole(ctx, state.ServerID.ValueString(), state.ID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete role", err)
	}
}

func (r *roleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitID(req.ID, 2, "server_id/role_id")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
