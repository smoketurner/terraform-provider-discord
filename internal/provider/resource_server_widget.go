package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &serverWidgetResource{}
	_ resource.ResourceWithImportState = &serverWidgetResource{}
	_ resource.ResourceWithIdentity    = &serverWidgetResource{}
)

type serverWidgetResource struct {
	resourceIdentity
	client *discord.Client
}

type serverWidgetModel struct {
	ID             types.String `tfsdk:"id"`
	ServerID       types.String `tfsdk:"server_id"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	ChannelID      types.String `tfsdk:"channel_id"`
	AuditLogReason types.String `tfsdk:"audit_log_reason"`
}

func newServerWidgetResource() resource.Resource {
	return &serverWidgetResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{serverIdentity("server_id", "id")}}}
}

func (r *serverWidgetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_widget"
}

func (r *serverWidgetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a server's widget settings. Each server has one widget, so destroying this resource " +
			"disables the widget and leaves the invite channel unchanged. Requires the Manage Server permission.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("Server ID."),
			"server_id":        serverIDAttribute(),
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the widget is enabled.",
				Required:            true,
			},
			"channel_id": schema.StringAttribute{
				MarkdownDescription: "Channel the widget's invite links to. Omit for no invite.",
				Optional:            true,
				Validators:          []validator.String{snowflakeValidator()},
			},
		},
	}
}

func (r *serverWidgetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *serverWidgetModel) apply(w *discord.WidgetSettings) {
	m.ID = m.ServerID
	m.Enabled = types.BoolValue(w.Enabled)
	m.ChannelID = stringPtrValue(w.ChannelID)
}

func (r *serverWidgetResource) write(ctx context.Context, m *serverWidgetModel) error {
	p := discord.Payload{}
	putBool(p, "enabled", m.Enabled)
	putString(p, "channel_id", m.ChannelID)
	w, err := r.client.ModifyWidgetSettings(ctx, m.ServerID.ValueString(), p)
	if err != nil {
		return err
	}
	m.apply(w)
	return nil
}

func (r *serverWidgetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan serverWidgetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	if err := r.write(ctx, &plan); err != nil {
		apiError(&resp.Diagnostics, "update server widget", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverWidgetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state serverWidgetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	w, err := r.client.GetWidgetSettings(ctx, state.ServerID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read server widget", err)
		return
	}
	state.apply(w)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serverWidgetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan serverWidgetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	if err := r.write(ctx, &plan); err != nil {
		apiError(&resp.Diagnostics, "update server widget", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverWidgetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverWidgetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	_, err := r.client.ModifyWidgetSettings(ctx, state.ServerID.ValueString(), discord.Payload{"enabled": false})
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "disable server widget", err)
	}
}
