package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// channelBase holds the attributes every channel resource shares.
type channelBase struct {
	ID       types.String `tfsdk:"id"`
	ServerID types.String `tfsdk:"server_id"`
	Name     types.String `tfsdk:"name"`
	Position types.Int64  `tfsdk:"position"`

	AuditLogReason types.String `tfsdk:"audit_log_reason"`
}

func (b *channelBase) payload(p discord.Payload) {
	putString(p, "name", b.Name)
}

func (b *channelBase) apply(ch *discord.Channel) {
	b.ID = types.StringValue(ch.ID)
	b.ServerID = types.StringValue(ch.GuildID)
	// Discord normalizes text channel names ("General Chat" becomes
	// "general-chat"); keep the configured spelling when it is equivalent.
	if b.Name.IsNull() || b.Name.IsUnknown() || normalizeChannelName(b.Name.ValueString()) != ch.Name {
		b.Name = types.StringValue(ch.Name)
	}
	b.Position = types.Int64Value(ch.Position)
}

func normalizeChannelName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), "-"))
}

// channelModel is implemented by pointers to each channel type's model.
type channelModel interface {
	base() *channelBase
	// payload returns the request body for the model's configurable fields.
	payload(ctx context.Context) (discord.Payload, diag.Diagnostics)
	// apply copies an API response into the model.
	apply(ctx context.Context, ch *discord.Channel) diag.Diagnostics
}

// channelKind describes one Discord channel type.
type channelKind struct {
	typeName    string
	channelType int
	description string
	attributes  map[string]schema.Attribute
}

type channelResource[T any, PT interface {
	*T
	channelModel
}] struct {
	kind   channelKind
	client *discord.Client
}

func (r *channelResource[T, PT]) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.kind.typeName
}

func (r *channelResource[T, PT]) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id":        idAttribute("Channel ID."),
		"server_id": serverIDAttribute(),
		"name": schema.StringAttribute{
			MarkdownDescription: "Channel name.",
			Required:            true,
			Validators:          []validator.String{stringvalidator.LengthBetween(1, 100)},
		},
		// No UseStateForUnknown: creating or moving other channels in the
		// same apply can shift this channel's position.
		"position": schema.Int64Attribute{
			MarkdownDescription: "Current sort position. Read-only; use `discord_channel_positions` to reorder channels.",
			Computed:            true,
		},
		"audit_log_reason": auditLogReasonAttribute(),
	}
	for k, v := range r.kind.attributes {
		attrs[k] = v
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: r.kind.description + " Permission overwrites are managed with `discord_channel_permission`.",
		Attributes:          attrs,
	}
}

// planAdjuster is implemented by channel models that correct the planned
// values of an update using the prior state.
type planAdjuster[PT any] interface {
	adjustPlan(ctx context.Context, prior PT) diag.Diagnostics
}

func (r *channelResource[T, PT]) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan, state T
	pm, sm := PT(&plan), PT(&state)
	a, ok := any(pm).(planAdjuster[PT])
	if !ok {
		return
	}
	resp.Diagnostics.Append(req.Plan.Get(ctx, pm)...)
	resp.Diagnostics.Append(req.State.Get(ctx, sm)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(a.adjustPlan(ctx, sm)...)
	resp.Diagnostics.Append(resp.Plan.Set(ctx, pm)...)
}

func (r *channelResource[T, PT]) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (r *channelResource[T, PT]) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan T
	m := PT(&plan)
	resp.Diagnostics.Append(req.Plan.Get(ctx, m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, m.base().AuditLogReason)
	p, diags := m.payload(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Discord treats an explicit null like an omitted field on create, but
	// omitting keeps request bodies minimal and avoids validation quirks.
	for k, v := range p {
		if v == nil {
			delete(p, k)
		}
	}
	p["type"] = r.kind.channelType
	ch, err := r.client.CreateChannel(ctx, m.base().ServerID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "create "+r.kind.typeName, err)
		return
	}
	resp.Diagnostics.Append(m.apply(ctx, ch)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *channelResource[T, PT]) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state T
	m := PT(&state)
	resp.Diagnostics.Append(req.State.Get(ctx, m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ch, err := r.client.GetChannel(ctx, m.base().ID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read "+r.kind.typeName, err)
		return
	}
	if ch.Type != r.kind.channelType {
		resp.Diagnostics.AddError("Unexpected channel type",
			fmt.Sprintf("Channel %s has type %d, but discord_%s manages type %d. Use the resource matching the channel's type.",
				ch.ID, ch.Type, r.kind.typeName, r.kind.channelType))
		return
	}
	resp.Diagnostics.Append(m.apply(ctx, ch)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *channelResource[T, PT]) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan, state T
	pm, sm := PT(&plan), PT(&state)
	resp.Diagnostics.Append(req.Plan.Get(ctx, pm)...)
	resp.Diagnostics.Append(req.State.Get(ctx, sm)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, pm.base().AuditLogReason)
	desired, diags := pm.payload(ctx)
	resp.Diagnostics.Append(diags...)
	current, diags := sm.payload(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ch, err := r.client.ModifyChannel(ctx, sm.base().ID.ValueString(), diffPayload(desired, current))
	if err != nil {
		apiError(&resp.Diagnostics, "update "+r.kind.typeName, err)
		return
	}
	resp.Diagnostics.Append(pm.apply(ctx, ch)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, pm)...)
}

func (r *channelResource[T, PT]) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state T
	m := PT(&state)
	resp.Diagnostics.Append(req.State.Get(ctx, m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, m.base().AuditLogReason)
	if err := r.client.DeleteChannel(ctx, m.base().ID.ValueString()); err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete "+r.kind.typeName, err)
	}
}

func (r *channelResource[T, PT]) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
