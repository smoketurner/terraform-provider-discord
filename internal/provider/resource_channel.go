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
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// channelBase holds the attributes every channel resource shares.
type channelBase struct {
	ID       types.String `tfsdk:"id"`
	ServerID types.String `tfsdk:"server_id"`
	Name     types.String `tfsdk:"name"`
	Position types.Int64  `tfsdk:"position"`

	// InitialPermissionOverwrites is only sent on create and is never read
	// back, so it cannot drift or conflict with discord_channel_permission.
	InitialPermissionOverwrites types.Set `tfsdk:"initial_permission_overwrites"`

	AuditLogReason types.String `tfsdk:"audit_log_reason"`
}

type initialOverwriteModel struct {
	ID    types.String `tfsdk:"id"`
	Type  types.String `tfsdk:"type"`
	Allow types.String `tfsdk:"allow"`
	Deny  types.String `tfsdk:"deny"`
}

// initialOverwrites returns the initial permission overwrites for the create
// request, or nil when there are none.
func (b *channelBase) initialOverwrites(ctx context.Context) ([]discord.Overwrite, diag.Diagnostics) {
	if b.InitialPermissionOverwrites.IsNull() || b.InitialPermissionOverwrites.IsUnknown() {
		return nil, nil
	}
	var models []initialOverwriteModel
	diags := b.InitialPermissionOverwrites.ElementsAs(ctx, &models, false)
	out := make([]discord.Overwrite, 0, len(models))
	for _, m := range models {
		t, _ := overwriteTypes.value(m.Type.ValueString())
		out = append(out, discord.Overwrite{
			ID: m.ID.ValueString(), Type: int(t), Allow: permissionsOrZero(m.Allow), Deny: permissionsOrZero(m.Deny),
		})
	}
	return out, diags
}

// permissionsOrZero matches Discord, which treats an omitted allow or deny as
// "0".
func permissionsOrZero(v types.String) string {
	if v.IsNull() {
		return "0"
	}
	return v.ValueString()
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

// channelFlags is the payload value for the channel flag bits a model
// manages. Discord replaces the whole bitfield, so the managed bits are merged
// into the channel's current flags before sending to keep flags set outside
// Terraform.
type channelFlags struct {
	Mask int64
	Set  int64
}

// with returns f also managing bit, which is set when on is true.
func (f channelFlags) with(bit int64, on bool) channelFlags {
	f.Mask |= bit
	if on {
		f.Set |= bit
	} else {
		f.Set &^= bit
	}
	return f
}

func (f channelFlags) merge(current int64) int64 {
	return current&^f.Mask | f.Set
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
	// convertible is the kind Discord can convert this kind to and from in
	// place, if any. Both kinds expose the channel's current type as the
	// "type" attribute.
	convertible *channelConversion
}

// channelConversion describes the other side of an in-place type conversion.
type channelConversion struct {
	typeName    string
	channelType int
	resource    func() resource.Resource
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
		"initial_permission_overwrites": initialPermissionOverwritesAttribute(),
		"audit_log_reason":              auditLogReasonAttribute(),
	}
	for k, v := range r.kind.attributes {
		attrs[k] = v
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: r.kind.description + " Permission overwrites are managed with `discord_channel_permission`; " +
			"`initial_permission_overwrites` only sets them when the channel is created.",
		Attributes: attrs,
	}
}

func initialPermissionOverwritesAttribute() schema.SetNestedAttribute {
	return schema.SetNestedAttribute{
		MarkdownDescription: "Permission overwrites sent in the request that creates the channel, so a private channel is " +
			"never visible without them. They are used only when the channel is created or replaced: Terraform does not " +
			"read them back, and changing this argument later only updates state, without calling Discord. Manage " +
			"overwrites after creation with `discord_channel_permission`; one with the same `overwrite_id` takes over the " +
			"initial overwrite and writes its own `allow` and `deny`. The bot can only allow or deny permissions it has " +
			"in the server, and only an Administrator can set `MANAGE_ROLES` in an overwrite.",
		Optional: true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"id": schema.StringAttribute{
					MarkdownDescription: "ID of the role or member the overwrite applies to. Use the server ID for `@everyone`.",
					Required:            true,
					Validators:          []validator.String{snowflakeValidator()},
				},
				"type": schema.StringAttribute{
					MarkdownDescription: "Overwrite target type: " + overwriteTypes.doc() + ".",
					Required:            true,
					Validators:          []validator.String{overwriteTypes.validator()},
				},
				"allow": schema.StringAttribute{
					MarkdownDescription: "Allowed permission bitfield as a decimal string. Omit for `0`.",
					Optional:            true,
					Validators:          []validator.String{permissionsValidator()},
				},
				"deny": schema.StringAttribute{
					MarkdownDescription: "Denied permission bitfield as a decimal string. Omit for `0`.",
					Optional:            true,
					Validators:          []validator.String{permissionsValidator()},
				},
			},
		},
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
	if f, ok := p["flags"].(channelFlags); ok {
		p["flags"] = f.merge(0)
	}
	overwrites, diags := m.base().initialOverwrites(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if overwrites != nil {
		p["permission_overwrites"] = overwrites
	}
	var followUp discord.Payload
	if s, ok := any(m).(createSplitter); ok {
		followUp = s.splitCreate(p)
	}
	p["type"] = r.kind.channelType
	ch, err := r.client.CreateChannel(ctx, m.base().ServerID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "create "+r.kind.typeName, err)
		return
	}
	resp.Diagnostics.Append(m.apply(ctx, ch)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
	if len(followUp) == 0 || resp.Diagnostics.HasError() {
		return
	}
	// The channel exists, so the state set above keeps it tracked (and
	// tainted) if the follow-up fails.
	ch, err = r.client.ModifyChannel(ctx, ch.ID, followUp)
	if err != nil {
		apiError(&resp.Diagnostics, "update new "+r.kind.typeName, err)
		return
	}
	resp.Diagnostics.Append(m.apply(ctx, ch)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// createSplitter is implemented by channel models with fields that Discord
// accepts on modify but not on create.
type createSplitter interface {
	// splitCreate removes those fields from the create payload and returns
	// the ones to send in a follow-up modify, if any.
	splitCreate(p discord.Payload) discord.Payload
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
	// A channel of the convertible type is accepted after a moved block or a
	// conversion outside Terraform; its "type" then shows the pending
	// conversion.
	if ch.Type != r.kind.channelType && (r.kind.convertible == nil || ch.Type != r.kind.convertible.channelType) {
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
	id := sm.base().ID.ValueString()
	if r.kind.convertible != nil {
		var currentType types.String
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("type"), &currentType)...)
		if resp.Diagnostics.HasError() {
			return
		}
		// Convert in a request of its own so the other changes are applied
		// to a channel of the new type.
		if currentType.ValueString() != convertibleChannelTypes.name(int64(r.kind.channelType)).ValueString() {
			if _, err := r.client.ModifyChannel(ctx, id, discord.Payload{"type": r.kind.channelType}); err != nil {
				apiError(&resp.Diagnostics, "convert channel to "+r.kind.typeName, err)
				return
			}
		}
	}
	diff := diffPayload(desired, current)
	// The model only holds the managed bits, so read the channel to learn the
	// rest.
	if f, ok := diff["flags"].(channelFlags); ok {
		ch, err := r.client.GetChannel(ctx, id)
		if err != nil {
			apiError(&resp.Diagnostics, "read "+r.kind.typeName+" flags", err)
			return
		}
		diff["flags"] = f.merge(ch.Flags)
	}
	var ch *discord.Channel
	var err error
	// Arguments that are never sent, such as initial_permission_overwrites,
	// can change alone; reading refreshes the computed attributes instead.
	if len(diff) == 0 {
		ch, err = r.client.GetChannel(ctx, id)
	} else {
		ch, err = r.client.ModifyChannel(ctx, id, diff)
	}
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

// MoveState lets a moved block change the resource type between kinds that
// Discord converts in place. Attributes the kinds share are copied; the
// refresh that follows fills in the rest, and the plan then shows the "type"
// change that Update sends.
func (r *channelResource[T, PT]) MoveState(ctx context.Context) []resource.StateMover {
	c := r.kind.convertible
	if c == nil {
		return nil
	}
	var source resource.SchemaResponse
	c.resource().Schema(ctx, resource.SchemaRequest{}, &source)
	return []resource.StateMover{{
		SourceSchema: &source.Schema,
		StateMover: func(ctx context.Context, req resource.MoveStateRequest, resp *resource.MoveStateResponse) {
			if req.SourceTypeName != "discord_"+c.typeName {
				return
			}
			if req.SourceState == nil {
				resp.Diagnostics.AddError("Unable to move channel state",
					"The discord_"+c.typeName+" state does not match the provider's schema for it.")
				return
			}
			var target resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &target)
			raw, err := copySharedAttributes(req.SourceState.Raw, target.Schema.Type().TerraformType(ctx))
			if err != nil {
				resp.Diagnostics.AddError("Unable to move channel state", err.Error())
				return
			}
			resp.TargetState = tfsdk.State{Schema: target.Schema, Raw: raw}
		},
	}}
}

// copySharedAttributes builds an object of the target type from the source
// object's attributes with the same name and type, leaving the others null.
func copySharedAttributes(source tftypes.Value, target tftypes.Type) (tftypes.Value, error) {
	var attrs map[string]tftypes.Value
	if err := source.As(&attrs); err != nil {
		return tftypes.Value{}, err
	}
	obj, ok := target.(tftypes.Object)
	if !ok {
		return tftypes.Value{}, fmt.Errorf("target type %s is not an object", target)
	}
	vals := make(map[string]tftypes.Value, len(obj.AttributeTypes))
	for name, t := range obj.AttributeTypes {
		if v, ok := attrs[name]; ok && v.Type().Equal(t) {
			vals[name] = v
		} else {
			vals[name] = tftypes.NewValue(t, nil)
		}
	}
	return tftypes.NewValue(obj, vals), nil
}

func (r *channelResource[T, PT]) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
