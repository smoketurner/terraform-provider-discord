package provider

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// maxIncidentAction is how far ahead Discord allows an incident action to
// end.
const maxIncidentAction = 24 * time.Hour

// clockSkew is added to the wall clock; tests move it forward to let
// incident actions expire without waiting.
var clockSkew atomic.Int64

func currentTime() time.Time {
	return time.Now().Add(time.Duration(clockSkew.Load()))
}

var (
	_ resource.ResourceWithConfigure   = &incidentActionsResource{}
	_ resource.ResourceWithImportState = &incidentActionsResource{}
	_ resource.ResourceWithIdentity    = &incidentActionsResource{}
	_ resource.ResourceWithModifyPlan  = &incidentActionsResource{}
)

type incidentActionsResource struct {
	resourceIdentity
	client *discord.Client
}

type incidentActionsModel struct {
	ID                   types.String `tfsdk:"id"`
	ServerID             types.String `tfsdk:"server_id"`
	InvitesDisabledUntil types.String `tfsdk:"invites_disabled_until"`
	DMsDisabledUntil     types.String `tfsdk:"dms_disabled_until"`
}

func newIncidentActionsResource() resource.Resource {
	return &incidentActionsResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{serverIdentity("server_id", "id")}}}
}

func (r *incidentActionsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_incident_actions"
}

func incidentActionAttribute(action string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "RFC 3339 timestamp until which " + action + ", at most 24 hours after the apply, e.g. " +
			"`time_offset.raid.rfc3339` from the `hashicorp/time` provider. Omit or set to null to lift the action. " +
			"Once the time passes the action ends on its own and the plan stays empty; set a new timestamp to start " +
			"it again.",
		Optional:   true,
		Validators: []validator.String{rfc3339Validator{}},
	}
}

func (r *incidentActionsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a server's incident actions, which pause invites or direct messages between " +
			"members for up to 24 hours during a raid. Actions end on their own when their time passes; that is not " +
			"treated as drift. An action lifted early outside Terraform is set again on the next apply. Destroying " +
			"the resource lifts both actions. To pause invites indefinitely, use `invites_disabled` on " +
			"`discord_server_settings`.",
		Attributes: map[string]schema.Attribute{
			"id":                     idAttribute("Server ID."),
			"server_id":              serverIDAttribute(),
			"invites_disabled_until": incidentActionAttribute("invites are paused"),
			"dms_disabled_until":     incidentActionAttribute("direct messages are paused"),
		},
	}
}

func (r *incidentActionsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *incidentActionsModel) actions() map[string]*types.String {
	return map[string]*types.String{
		"invites_disabled_until": &m.InvitesDisabledUntil,
		"dms_disabled_until":     &m.DMsDisabledUntil,
	}
}

// ModifyPlan checks new timestamps against the current time, which config
// validation cannot do: a timestamp that has passed stays valid in
// configuration once applied.
func (r *incidentActionsResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan, state incidentActionsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	prior := state.actions()
	now := currentTime()
	for name, v := range plan.actions() {
		if !isSet(*v) || v.Equal(*prior[name]) {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, v.ValueString())
		switch {
		case err != nil:
		case !t.After(now):
			resp.Diagnostics.AddAttributeError(path.Root(name), "Invalid argument",
				fmt.Sprintf("%s must be in the future, got %s.", name, v.ValueString()))
		case t.After(now.Add(maxIncidentAction)):
			resp.Diagnostics.AddAttributeError(path.Root(name), "Invalid argument",
				fmt.Sprintf("%s must be at most 24 hours from now, got %s.", name, v.ValueString()))
		}
	}
}

// payload sends each active action and lifts the rest. An action whose time
// has passed is sent as null, since Discord rejects past timestamps.
func (m *incidentActionsModel) payload() discord.Payload {
	p := discord.Payload{}
	now := currentTime()
	for name, v := range m.actions() {
		p[name] = nil
		if t, ok := parseTimestamp(*v); ok && t.After(now) {
			p[name] = v.ValueString()
		}
	}
	return p
}

// apply records the actions Discord reports. A timestamp equal to the
// configured one keeps the configured form; an action Discord no longer
// reports keeps its timestamp once that has passed, because it expired
// rather than being lifted.
func (m *incidentActionsModel) apply(d *discord.IncidentsData) {
	if d == nil {
		d = &discord.IncidentsData{}
	}
	current := map[string]*string{
		"invites_disabled_until": d.InvitesDisabledUntil,
		"dms_disabled_until":     d.DMsDisabledUntil,
	}
	now := currentTime()
	for name, v := range m.actions() {
		prior, priorOK := parseTimestamp(*v)
		switch cur := current[name]; {
		case cur == nil && priorOK && !prior.After(now):
		case cur == nil:
			*v = types.StringNull()
		default:
			if t, err := time.Parse(time.RFC3339Nano, *cur); err != nil || !priorOK || !sameInstant(t, prior) {
				*v = types.StringValue(*cur)
			}
		}
	}
}

func parseTimestamp(v types.String) (time.Time, bool) {
	if !isSet(v) {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, v.ValueString())
	return t, err == nil
}

// sameInstant compares timestamps to the millisecond, the precision Discord
// keeps.
func sameInstant(a, b time.Time) bool {
	return a.Truncate(time.Millisecond).Equal(b.Truncate(time.Millisecond))
}

func (r *incidentActionsResource) write(ctx context.Context, m *incidentActionsModel) error {
	d, err := r.client.ModifyGuildIncidentActions(ctx, m.ServerID.ValueString(), m.payload())
	if err != nil {
		return err
	}
	m.ID = m.ServerID
	m.apply(d)
	return nil
}

func (r *incidentActionsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan incidentActionsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &plan); err != nil {
		apiError(&resp.Diagnostics, "set incident actions", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *incidentActionsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state incidentActionsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetGuild(ctx, state.ServerID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read server", err)
		return
	}
	state.ID = state.ServerID
	state.apply(g.IncidentsData)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *incidentActionsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan incidentActionsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &plan); err != nil {
		apiError(&resp.Diagnostics, "set incident actions", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *incidentActionsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state incidentActionsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.ModifyGuildIncidentActions(ctx, state.ServerID.ValueString(),
		discord.Payload{"invites_disabled_until": nil, "dms_disabled_until": nil})
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "lift incident actions", err)
	}
}

// rfc3339Validator accepts RFC 3339 timestamps.
type rfc3339Validator struct{}

func (rfc3339Validator) Description(context.Context) string {
	return "must be an RFC 3339 timestamp, e.g. 2006-01-02T15:04:05Z"
}

func (v rfc3339Validator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v rfc3339Validator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !isSet(req.ConfigValue) {
		return
	}
	if _, err := time.Parse(time.RFC3339Nano, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid timestamp",
			fmt.Sprintf("Value %s, got %q.", v.Description(ctx), req.ConfigValue.ValueString()))
	}
}
