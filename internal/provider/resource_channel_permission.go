package provider

import (
	"context"

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
	_ resource.ResourceWithIdentity    = &channelPermissionResource{}
)

type channelPermissionResource struct {
	resourceIdentity
	client *discord.Client
}

type channelPermissionModel struct {
	ID             types.String `tfsdk:"id"`
	ChannelID      types.String `tfsdk:"channel_id"`
	OverwriteID    types.String `tfsdk:"overwrite_id"`
	Type           types.String `tfsdk:"type"`
	Allow          types.String `tfsdk:"allow"`
	Deny           types.String `tfsdk:"deny"`
	AuditLogReason types.String `tfsdk:"audit_log_reason"`
}

func newChannelPermissionResource() resource.Resource {
	return &channelPermissionResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		channelIdentity("channel_id"),
		{name: "overwrite_id", description: "ID of the role or member the overwrite applies to.", state: []string{"overwrite_id"}},
	}}}
}

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
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("`channel_id/overwrite_id`."),
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
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan channelPermissionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	if err := r.write(ctx, &plan); err != nil {
		apiError(&resp.Diagnostics, "set channel permission", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *channelPermissionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
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
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan channelPermissionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
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
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	err := r.client.DeleteChannelPermission(ctx, state.ChannelID.ValueString(), state.OverwriteID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete channel permission", err)
	}
}
