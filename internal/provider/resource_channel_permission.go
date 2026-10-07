package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var overwriteTypes = enumMapping{"role", "member"}

var (
	_ resource.ResourceWithConfigure   = &channelPermissionResource{}
	_ resource.ResourceWithImportState = &channelPermissionResource{}
)

type channelPermissionResource struct {
	client *discord.Client
}

type channelPermissionModel struct {
	ID          types.String `tfsdk:"id"`
	ChannelID   types.String `tfsdk:"channel_id"`
	OverwriteID types.String `tfsdk:"overwrite_id"`
	Type        types.String `tfsdk:"type"`
	Allow       types.String `tfsdk:"allow"`
	Deny        types.String `tfsdk:"deny"`
}

func newChannelPermissionResource() resource.Resource { return &channelPermissionResource{} }

func (r *channelPermissionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_channel_permission"
}

func (r *channelPermissionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one permission overwrite on a channel for a role or member. The overwrite is " +
			"written in a single request, so permissions are never temporarily removed. If the overwrite is deleted " +
			"outside Terraform it is recreated on the next apply.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("`channel_id/overwrite_id`."),
			"channel_id": schema.StringAttribute{
				MarkdownDescription: "ID of the channel.",
				Required:            true,
				Validators:          []validator.String{snowflakeValidator()},
				PlanModifiers:       replace,
			},
			"overwrite_id": schema.StringAttribute{
				MarkdownDescription: "ID of the role or member the overwrite applies to. Use the server ID for `@everyone`.",
				Required:            true,
				Validators:          []validator.String{snowflakeValidator()},
				PlanModifiers:       replace,
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Overwrite target type: " + overwriteTypes.doc() + ".",
				Required:            true,
				Validators:          []validator.String{overwriteTypes.validator()},
				PlanModifiers:       replace,
			},
			"allow": schema.StringAttribute{
				MarkdownDescription: "Allowed permission bitfield as a decimal string. Defaults to `0`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("0"),
				Validators:          []validator.String{permissionsValidator()},
			},
			"deny": schema.StringAttribute{
				MarkdownDescription: "Denied permission bitfield as a decimal string. Defaults to `0`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("0"),
				Validators:          []validator.String{permissionsValidator()},
			},
		},
	}
}

func (r *channelPermissionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (r *channelPermissionResource) write(ctx context.Context, m *channelPermissionModel) error {
	t, _ := overwriteTypes.value(m.Type.ValueString())
	err := r.client.EditChannelPermission(ctx, m.ChannelID.ValueString(), discord.Overwrite{
		ID: m.OverwriteID.ValueString(), Type: int(t), Allow: m.Allow.ValueString(), Deny: m.Deny.ValueString(),
	})
	m.ID = types.StringValue(m.ChannelID.ValueString() + "/" + m.OverwriteID.ValueString())
	return err
}

func (r *channelPermissionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan channelPermissionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &plan); err != nil {
		apiError(&resp.Diagnostics, "set channel permission", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *channelPermissionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state channelPermissionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ch, err := r.client.GetChannel(ctx, state.ChannelID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read channel", err)
		return
	}
	for _, o := range ch.PermissionOverwrites {
		if o.ID == state.OverwriteID.ValueString() {
			state.Type = overwriteTypes.name(int64(o.Type))
			state.Allow = types.StringValue(o.Allow)
			state.Deny = types.StringValue(o.Deny)
			state.ID = types.StringValue(ch.ID + "/" + o.ID)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *channelPermissionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan channelPermissionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &plan); err != nil {
		apiError(&resp.Diagnostics, "set channel permission", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *channelPermissionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state channelPermissionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteChannelPermission(ctx, state.ChannelID.ValueString(), state.OverwriteID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete channel permission", err)
	}
}

func (r *channelPermissionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitID(req.ID, 2, "channel_id/overwrite_id")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("channel_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("overwrite_id"), parts[1])...)
}
