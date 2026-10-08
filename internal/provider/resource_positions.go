package provider

import (
	"context"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// positionsKind adapts the shared ordering resource to roles or channels.
type positionsKind struct {
	typeName    string
	description string
	idsAttr     string
	idsDesc     string
	// descending is true when the list is ordered from highest position to
	// lowest, as roles are displayed in the Discord client.
	descending bool
	list       func(ctx context.Context, c *discord.Client, serverID string) ([]discord.Positioned, error)
	apply      func(ctx context.Context, c *discord.Client, serverID string, updates []discord.PositionUpdate) error

	// audited is true when Discord records an audit log reason for the
	// reorder; it does not for channel positions.
	audited bool
}

var _ resource.ResourceWithConfigure = &positionsResource{}

type positionsResource struct {
	kind   positionsKind
	client *discord.Client
}

type positionsModel struct {
	ID       types.String `tfsdk:"id"`
	ServerID types.String `tfsdk:"server_id"`
	IDs      types.List   `tfsdk:"-"`

	AuditLogReason types.String `tfsdk:"-"`
}

func newRolePositionsResource() resource.Resource {
	return &positionsResource{kind: positionsKind{
		typeName: "_role_positions",
		description: "Orders a set of server roles atomically with a single API request. Roles that are not listed keep " +
			"their place: the listed roles are rearranged among the positions they already occupy. The bot can only " +
			"move roles below its own highest role.",
		idsAttr:    "role_ids",
		idsDesc:    "Role IDs ordered from highest to lowest, as shown in the Discord client.",
		descending: true,
		audited:    true,
		list: func(ctx context.Context, c *discord.Client, serverID string) ([]discord.Positioned, error) {
			roles, err := c.ListRoles(ctx, serverID)
			out := make([]discord.Positioned, 0, len(roles))
			for _, r := range roles {
				out = append(out, discord.Positioned{ID: r.ID, Position: r.Position})
			}
			return out, err
		},
		apply: func(ctx context.Context, c *discord.Client, serverID string, updates []discord.PositionUpdate) error {
			return c.ModifyRolePositions(ctx, serverID, updates)
		},
	}}
}

func newChannelPositionsResource() resource.Resource {
	return &positionsResource{kind: positionsKind{
		typeName: "_channel_positions",
		description: "Orders a set of channels atomically with a single API request. Channels that are not listed keep " +
			"their place: the listed channels are rearranged among the positions they already occupy. List channels " +
			"that share a parent category (or categories themselves) to control how they are displayed.",
		idsAttr: "channel_ids",
		idsDesc: "Channel IDs ordered from top to bottom, as shown in the Discord client.",
		list: func(ctx context.Context, c *discord.Client, serverID string) ([]discord.Positioned, error) {
			channels, err := c.ListChannels(ctx, serverID)
			out := make([]discord.Positioned, 0, len(channels))
			for _, ch := range channels {
				out = append(out, discord.Positioned{ID: ch.ID, Position: ch.Position})
			}
			return out, err
		},
		apply: func(ctx context.Context, c *discord.Client, serverID string, updates []discord.PositionUpdate) error {
			return c.ModifyChannelPositions(ctx, serverID, updates)
		},
	}}
}

func (r *positionsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + r.kind.typeName
}

func (r *positionsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: r.kind.description + " Destroying this resource leaves the current order unchanged.",
		Attributes: map[string]schema.Attribute{
			"id":        idAttribute("Server ID."),
			"server_id": serverIDAttribute(),
			r.kind.idsAttr: schema.ListAttribute{
				MarkdownDescription: r.kind.idsDesc,
				ElementType:         types.StringType,
				Required:            true,
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
					listvalidator.UniqueValues(),
					listvalidator.ValueStringsAre(snowflakeValidator()),
				},
			},
		},
	}
	if r.kind.audited {
		resp.Schema.Attributes[auditLogReasonAttr] = auditLogReasonAttribute()
	}
}

func (r *positionsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

// The ID list attribute name differs per kind, so it is accessed by path
// instead of through the struct.
func (r *positionsResource) get(ctx context.Context, src interface {
	Get(context.Context, any) diag.Diagnostics
	GetAttribute(context.Context, path.Path, any) diag.Diagnostics
}, m *positionsModel) diag.Diagnostics {
	var diags diag.Diagnostics
	diags.Append(src.GetAttribute(ctx, path.Root("id"), &m.ID)...)
	diags.Append(src.GetAttribute(ctx, path.Root("server_id"), &m.ServerID)...)
	diags.Append(src.GetAttribute(ctx, path.Root(r.kind.idsAttr), &m.IDs)...)
	if r.kind.audited {
		diags.Append(src.GetAttribute(ctx, path.Root(auditLogReasonAttr), &m.AuditLogReason)...)
	}
	return diags
}

func (r *positionsResource) set(ctx context.Context, dst interface {
	SetAttribute(context.Context, path.Path, any) diag.Diagnostics
}, m *positionsModel) diag.Diagnostics {
	var diags diag.Diagnostics
	diags.Append(dst.SetAttribute(ctx, path.Root("id"), m.ID)...)
	diags.Append(dst.SetAttribute(ctx, path.Root("server_id"), m.ServerID)...)
	diags.Append(dst.SetAttribute(ctx, path.Root(r.kind.idsAttr), m.IDs)...)
	if r.kind.audited {
		diags.Append(dst.SetAttribute(ctx, path.Root(auditLogReasonAttr), m.AuditLogReason)...)
	}
	return diags
}

// ascending converts the configured order to ascending position order.
func (r *positionsResource) ascending(ids []string) []string {
	out := slices.Clone(ids)
	if r.kind.descending {
		slices.Reverse(out)
	}
	return out
}

func (r *positionsResource) write(ctx context.Context, m *positionsModel) diag.Diagnostics {
	var diags diag.Diagnostics
	var ids []string
	diags.Append(m.IDs.ElementsAs(ctx, &ids, false)...)
	if diags.HasError() {
		return diags
	}
	serverID := m.ServerID.ValueString()
	ctx = withAuditLogReason(ctx, m.AuditLogReason)
	current, err := r.kind.list(ctx, r.client, serverID)
	if err != nil {
		apiError(&diags, "list current positions", err)
		return diags
	}
	known := make(map[string]bool, len(current))
	for _, p := range current {
		known[p.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			diags.AddAttributeError(path.Root(r.kind.idsAttr), "Unknown ID",
				fmt.Sprintf("%s does not exist in server %s.", id, serverID))
		}
	}
	if diags.HasError() {
		return diags
	}
	if updates := discord.Reorder(current, r.ascending(ids)); len(updates) > 0 {
		if err := r.kind.apply(ctx, r.client, serverID, updates); err != nil {
			apiError(&diags, "update positions", err)
			return diags
		}
	}
	m.ID = m.ServerID
	return diags
}

func (r *positionsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan positionsModel
	resp.Diagnostics.Append(r.get(ctx, req.Plan, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.write(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.set(ctx, &resp.State, &plan)...)
}

func (r *positionsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state positionsModel
	resp.Diagnostics.Append(r.get(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.kind.list(ctx, r.client, state.ServerID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "list current positions", err)
		return
	}
	var ids []string
	resp.Diagnostics.Append(state.IDs.ElementsAs(ctx, &ids, false)...)
	order := discord.OrderOf(current, ids)
	if r.kind.descending {
		slices.Reverse(order)
	}
	state.IDs = stringListValue(ctx, order, &resp.Diagnostics)
	resp.Diagnostics.Append(r.set(ctx, &resp.State, &state)...)
}

func (r *positionsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.kind.audited && updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan positionsModel
	resp.Diagnostics.Append(r.get(ctx, req.Plan, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.write(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.set(ctx, &resp.State, &plan)...)
}

func (r *positionsResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}
