package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/boolvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// computedPermissionsDataSource reads the permissions Discord computes for a
// member or a role, at the server level or in a channel.
type computedPermissionsDataSource struct {
	readOnlyDataSource
	typeName    string
	description string
	// idAttribute names the member or role ID attribute; idDescription
	// documents it.
	idAttribute   string
	idDescription string
	// implicitNote is appended to the implicit attribute's description.
	implicitNote string
	// action describes the read in error messages.
	action  string
	server  func(c *discord.Client, ctx context.Context, serverID, id string) (string, error)
	channel func(c *discord.Client, ctx context.Context, channelID, id string, implicit bool) (string, error)
}

const computedPermissionsChannelDoc = "When `channel_id` is set, the channel's permission overwrites are applied; threads use " +
	"their parent channel's overwrites. Reading a channel requires the bot to have `VIEW_CHANNEL` there, " +
	"and `CONNECT` as well in voice and stage channels."

func newMemberPermissionsDataSource() datasource.DataSource {
	return &computedPermissionsDataSource{
		typeName: "_member_permissions",
		description: "Reads a member's permissions as Discord computes them from all of the member's roles and `@everyone`, " +
			"including member-specific overwrites in a channel. " + computedPermissionsChannelDoc + " " +
			"See [Get Guild Member Permissions](https://docs.discord.com/developers/resources/guild#get-guild-member-permissions) and " +
			"[Get Channel Member Permissions](https://docs.discord.com/developers/resources/channel#get-channel-member-permissions).",
		idAttribute:   "user_id",
		idDescription: "ID of the member's user.",
		implicitNote:  " Discord never masks the permissions of bot members.",
		action:        "read member permissions",
		server:        (*discord.Client).GetGuildMemberPermissions,
		channel:       (*discord.Client).GetChannelMemberPermissions,
	}
}

func newRolePermissionsDataSource() datasource.DataSource {
	return &computedPermissionsDataSource{
		typeName: "_role_permissions",
		description: "Reads the permissions a role grants together with `@everyone`, as Discord computes them. " +
			"Other roles and member-specific overwrites are not included. " + computedPermissionsChannelDoc + " " +
			"See [Get Guild Role Permissions](https://docs.discord.com/developers/resources/guild#get-guild-role-permissions) and " +
			"[Get Channel Role Permissions](https://docs.discord.com/developers/resources/channel#get-channel-role-permissions).",
		idAttribute:   "role_id",
		idDescription: "ID of the role.",
		action:        "read role permissions",
		server:        (*discord.Client).GetGuildRolePermissions,
		channel:       (*discord.Client).GetChannelRolePermissions,
	}
}

func (d *computedPermissionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + d.typeName
}

func (d *computedPermissionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: d.description,
		Attributes: map[string]schema.Attribute{
			"server_id":   dsServerID(),
			d.idAttribute: requiredSnowflake(d.idDescription),
			"channel_id":  optionalSnowflake("ID of a channel or thread to compute the permissions in. Omit for server-level permissions."),
			"implicit": schema.BoolAttribute{
				MarkdownDescription: "Whether Discord masks the permissions that a missing `VIEW_CHANNEL`, or a missing `CONNECT` in a " +
					"voice or stage channel, makes unusable (see [implicit permissions](https://docs.discord.com/developers/topics/permissions#implicit-permissions)). " +
					"Requires `channel_id`. Defaults to `true`." + d.implicitNote,
				Optional:   true,
				Validators: []validator.Bool{boolvalidator.AlsoRequires(path.MatchRoot("channel_id"))},
			},
			"permissions": computedString("The computed permissions as a decimal bitfield string."),
			"permission_names": schema.SetAttribute{
				MarkdownDescription: "Names of the permission flags set in `permissions`, such as `VIEW_CHANNEL`. " +
					"Flags this provider does not know are omitted.",
				ElementType: types.StringType,
				Computed:    true,
			},
		},
	}
}

func (d *computedPermissionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var serverID, id, channelID types.String
	var implicit types.Bool
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("server_id"), &serverID)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(d.idAttribute), &id)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("channel_id"), &channelID)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("implicit"), &implicit)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var bits string
	var err error
	if channelID.IsNull() {
		bits, err = d.server(d.client, ctx, serverID.ValueString(), id.ValueString())
	} else {
		bits, err = d.channel(d.client, ctx, channelID.ValueString(), id.ValueString(), implicit.IsNull() || implicit.ValueBool())
	}
	if err != nil {
		apiError(&resp.Diagnostics, d.action, err)
		return
	}
	names, err := discord.PermissionNamesFromBits(bits)
	if err != nil {
		resp.Diagnostics.AddError("Invalid permissions from Discord", err.Error())
		return
	}
	resp.State.Raw = req.Config.Raw
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("permissions"), types.StringValue(bits))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("permission_names"), stringSetValue(ctx, names, &resp.Diagnostics))...)
}
