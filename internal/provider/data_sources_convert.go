package provider

import (
	"context"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// These data sources do the same conversions as the provider functions, for
// Terraform versions before 1.8 that cannot call provider functions.

// Permissions.

type permissionsDataSource struct{}

type permissionsDataModel struct {
	Allow     types.Set    `tfsdk:"allow"`
	Deny      types.Set    `tfsdk:"deny"`
	AllowBits types.String `tfsdk:"allow_bits"`
	DenyBits  types.String `tfsdk:"deny_bits"`
}

func newPermissionsDataSource() datasource.DataSource { return permissionsDataSource{} }

func (permissionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_permissions"
}

func (permissionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	names := "Valid names: " + strings.Join(backtick(discord.PermissionNames()), ", ") + "."
	resp.Schema = schema.Schema{
		MarkdownDescription: "Combines Discord permission flag names (case-insensitive) into the decimal strings used by " +
			"`permissions`, `allow` and `deny` attributes. On Terraform 1.8 and later, prefer the " +
			"`provider::discord::permissions` function. " + names,
		Attributes: map[string]schema.Attribute{
			"allow": schema.SetAttribute{
				MarkdownDescription: "Permission names to allow, e.g. `[\"VIEW_CHANNEL\", \"SEND_MESSAGES\"]`.",
				ElementType:         types.StringType,
				Optional:            true,
			},
			"deny": schema.SetAttribute{
				MarkdownDescription: "Permission names to deny. A name cannot be in both `allow` and `deny`.",
				ElementType:         types.StringType,
				Optional:            true,
			},
			"allow_bits": computedString("Bitfield of `allow` as a decimal string, `0` when `allow` is empty or unset."),
			"deny_bits":  computedString("Bitfield of `deny` as a decimal string, `0` when `deny` is empty or unset."),
		},
	}
}

func (permissionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m permissionsDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var allow, deny []string
	resp.Diagnostics.Append(m.Allow.ElementsAs(ctx, &allow, false)...)
	resp.Diagnostics.Append(m.Deny.ElementsAs(ctx, &deny, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	allowBits, err := discord.PermissionBits(allow)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("allow"), "Invalid permission", err.Error())
	}
	denyBits, err := discord.PermissionBits(deny)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("deny"), "Invalid permission", err.Error())
	}
	if resp.Diagnostics.HasError() {
		return
	}
	// PermissionBits always returns a valid decimal bitfield.
	a, _ := strconv.ParseUint(allowBits, 10, 64)
	d, _ := strconv.ParseUint(denyBits, 10, 64)
	if a&d != 0 {
		both, _ := discord.PermissionNamesFromBits(strconv.FormatUint(a&d, 10))
		resp.Diagnostics.AddAttributeError(path.Root("deny"), "Conflicting permissions",
			"Permissions in both allow and deny: "+strings.Join(both, ", ")+".")
		return
	}
	m.AllowBits = types.StringValue(allowBits)
	m.DenyBits = types.StringValue(denyBits)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Color.

type colorDataSource struct{}

type colorDataModel struct {
	Hex   types.String `tfsdk:"hex"`
	RGB   types.List   `tfsdk:"rgb"`
	Color types.Int64  `tfsdk:"color"`
}

func newColorDataSource() datasource.DataSource { return colorDataSource{} }

func (colorDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_color"
}

func (colorDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("hex"), path.MatchRoot("rgb")),
	}
}

func (colorDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Converts a hex or RGB color to the integer Discord uses for role and embed colors. On " +
			"Terraform 1.8 and later, prefer the `provider::discord::color` function.",
		Attributes: map[string]schema.Attribute{
			"hex": schema.StringAttribute{
				MarkdownDescription: "Hex color such as `#5865F2`, `5865f2` or `#fff`. Exactly one of `hex` or `rgb` is required.",
				Optional:            true,
			},
			"rgb": schema.ListAttribute{
				MarkdownDescription: "Red, green and blue components, each 0-255, e.g. `[88, 101, 242]`.",
				ElementType:         types.Int64Type,
				Optional:            true,
				Validators: []validator.List{
					listvalidator.SizeBetween(3, 3),
					listvalidator.ValueInt64sAre(int64validator.Between(0, 255)),
				},
			},
			"color": computedInt("Color as an integer."),
		},
	}
}

func (colorDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m colorDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.Hex.IsNull() {
		v, err := parseHexColor(m.Hex.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("hex"), "Invalid color", err.Error())
			return
		}
		m.Color = types.Int64Value(v)
	} else {
		var rgb []int64
		resp.Diagnostics.Append(m.RGB.ElementsAs(ctx, &rgb, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		m.Color = types.Int64Value(rgb[0]<<16 | rgb[1]<<8 | rgb[2])
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
