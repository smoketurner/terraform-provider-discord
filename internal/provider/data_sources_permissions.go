package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// computedPermissionsDataSource reads the permissions Discord computes for a
// member or a role, in a server or in a channel.
type computedPermissionsDataSource struct {
	readOnlyDataSource
	typeName    string
	description string
	// scopeAttribute is server_id or channel_id, and idAttribute user_id or
	// role_id; idDescription documents idAttribute.
	scopeAttribute string
	idAttribute    string
	idDescription  string
	// implicitNote is appended to the implicit attribute's description. The
	// attribute only exists on channel data sources.
	implicitNote string
	// action describes the read in error messages.
	action string
	read   func(c *discord.Client, ctx context.Context, scopeID, id string, implicit bool) (string, error)
}

const channelPermissionsDoc = " Threads use their parent channel's overwrites. The bot needs `VIEW_CHANNEL` in the channel, " +
	"and `CONNECT` as well in voice and stage channels."

func newServerMemberPermissionsDataSource() datasource.DataSource {
	return &computedPermissionsDataSource{
		typeName: "_server_member_permissions",
		description: "Reads a member's server-level permissions as Discord computes them from all of the member's roles and `@everyone`. " +
			"See [Get Guild Member Permissions](https://docs.discord.com/developers/resources/guild#get-guild-member-permissions).",
		scopeAttribute: "server_id",
		idAttribute:    "user_id",
		idDescription:  "ID of the member's user.",
		action:         "read server member permissions",
		read: func(c *discord.Client, ctx context.Context, serverID, userID string, _ bool) (string, error) {
			return c.GetGuildMemberPermissions(ctx, serverID, userID)
		},
	}
}

func newServerRolePermissionsDataSource() datasource.DataSource {
	return &computedPermissionsDataSource{
		typeName: "_server_role_permissions",
		description: "Reads the server-level permissions a role grants together with `@everyone`, as Discord computes them. " +
			"Other roles are not included. " +
			"See [Get Guild Role Permissions](https://docs.discord.com/developers/resources/guild#get-guild-role-permissions).",
		scopeAttribute: "server_id",
		idAttribute:    "role_id",
		idDescription:  "ID of the role.",
		action:         "read server role permissions",
		read: func(c *discord.Client, ctx context.Context, serverID, roleID string, _ bool) (string, error) {
			return c.GetGuildRolePermissions(ctx, serverID, roleID)
		},
	}
}

func newChannelMemberPermissionsDataSource() datasource.DataSource {
	return &computedPermissionsDataSource{
		typeName: "_channel_member_permissions",
		description: "Reads a member's permissions in a channel as Discord computes them from all of the member's roles and `@everyone`, " +
			"applying the channel's overwrites for them and for the member." + channelPermissionsDoc + " " +
			"See [Get Channel Member Permissions](https://docs.discord.com/developers/resources/channel#get-channel-member-permissions).",
		scopeAttribute: "channel_id",
		idAttribute:    "user_id",
		idDescription:  "ID of the member's user.",
		implicitNote:   " Discord never masks the permissions of bot members.",
		action:         "read channel member permissions",
		read:           (*discord.Client).GetChannelMemberPermissions,
	}
}

func newChannelRolePermissionsDataSource() datasource.DataSource {
	return &computedPermissionsDataSource{
		typeName: "_channel_role_permissions",
		description: "Reads the permissions a role grants together with `@everyone` in a channel, as Discord computes them, " +
			"applying the channel's overwrites for both. Other roles and member-specific overwrites are not included." +
			channelPermissionsDoc + " " +
			"See [Get Channel Role Permissions](https://docs.discord.com/developers/resources/channel#get-channel-role-permissions).",
		scopeAttribute: "channel_id",
		idAttribute:    "role_id",
		idDescription:  "ID of the role.",
		action:         "read channel role permissions",
		read:           (*discord.Client).GetChannelRolePermissions,
	}
}

func (d *computedPermissionsDataSource) channel() bool { return d.scopeAttribute == "channel_id" }

func (d *computedPermissionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + d.typeName
}

func (d *computedPermissionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		d.idAttribute: requiredSnowflake(d.idDescription),
		"permissions": computedString("The computed permissions as a decimal bitfield string."),
		"permission_names": schema.SetAttribute{
			MarkdownDescription: "Names of the permission flags set in `permissions`, such as `VIEW_CHANNEL`. " +
				"Flags this provider does not know are omitted.",
			ElementType: types.StringType,
			Computed:    true,
		},
	}
	if d.channel() {
		attrs["channel_id"] = requiredSnowflake("ID of the channel or thread.")
		attrs["implicit"] = schema.BoolAttribute{
			MarkdownDescription: "Whether Discord masks the permissions that a missing `VIEW_CHANNEL`, or a missing `CONNECT` in a " +
				"voice or stage channel, makes unusable (see [implicit permissions](https://docs.discord.com/developers/topics/permissions#implicit-permissions)). " +
				"Defaults to `true`." + d.implicitNote,
			Optional: true,
		}
	} else {
		attrs["server_id"] = dsServerID()
	}
	resp.Schema = schema.Schema{MarkdownDescription: d.description, Attributes: attrs}
}

func (d *computedPermissionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var scopeID, id types.String
	implicit := types.BoolNull()
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(d.scopeAttribute), &scopeID)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(d.idAttribute), &id)...)
	if d.channel() {
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("implicit"), &implicit)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	bits, err := d.read(d.client, ctx, scopeID.ValueString(), id.ValueString(), implicit.IsNull() || implicit.ValueBool())
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
