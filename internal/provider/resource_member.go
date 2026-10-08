package provider

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure   = &memberResource{}
	_ resource.ResourceWithImportState = &memberResource{}
	_ resource.ResourceWithIdentity    = &memberResource{}
)

// maxTimeout is how far ahead Discord accepts communication_disabled_until.
const maxTimeout = 28 * 24 * time.Hour

// Discord trims leading and trailing whitespace from nicknames, so such a
// nickname would never match what is read back.
var untrimmedRegexp = regexp.MustCompile(`^\S(.*\S)?$`)

type memberResource struct {
	resourceIdentity
	client *discord.Client
}

type memberModel struct {
	ID                         types.String `tfsdk:"id"`
	ServerID                   types.String `tfsdk:"server_id"`
	UserID                     types.String `tfsdk:"user_id"`
	Nick                       types.String `tfsdk:"nick"`
	CommunicationDisabledUntil types.String `tfsdk:"communication_disabled_until"`
	AuditLogReason             types.String `tfsdk:"audit_log_reason"`
}

func newMemberResource() resource.Resource {
	return &memberResource{resourceIdentity: memberIdentity()}
}

func (r *memberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_member"
}

func (r *memberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the nickname and timeout of an existing server member. Adding members to a server " +
			"needs a user's OAuth2 token, so the member must already have joined; destroying the resource does not " +
			"remove them.\n\n" +
			"Each argument is managed only while it is set. Removing an argument from the configuration, or destroying " +
			"the resource, clears the nickname and ends an active timeout. Arguments that were never set are left " +
			"alone, except that importing reads the current nickname and any active timeout.\n\n" +
			"Voice mute and deafen are not managed: Discord rejects them unless the member is connected to a voice " +
			"channel. Discord has a separate endpoint for the bot's own nickname (Modify Current Member, which needs " +
			"`CHANGE_NICKNAME`); this resource does not use it.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttribute(),
			"id":               idAttribute("`server_id/user_id`."),
			"server_id":        serverIDAttribute(),
			"user_id":          memberUserIDAttribute(),
			"nick": schema.StringAttribute{
				MarkdownDescription: "The member's nickname in the server, 1 to 32 characters without leading or trailing " +
					"whitespace. Requires `MANAGE_NICKNAMES`.",
				Optional: true,
				Validators: []validator.String{
					stringvalidator.UTF8LengthBetween(1, 32),
					stringvalidator.RegexMatches(untrimmedRegexp, "must not start or end with whitespace"),
				},
			},
			"communication_disabled_until": schema.StringAttribute{
				MarkdownDescription: "When the member's timeout ends, as an RFC 3339 timestamp such as " +
					"`2030-01-01T00:00:00Z`, at most 28 days in the future. Requires `MODERATE_MEMBERS`; Discord refuses to time out the server owner " +
					"and administrators. Once the time has passed the timeout has expired, which is not a change: a time " +
					"in the past means no timeout. If the timeout is removed early outside Terraform, the next apply " +
					"sets it again.",
				Optional:   true,
				Validators: []validator.String{timeoutValidator{}},
			},
		},
	}
}

func (r *memberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (m *memberModel) id() string {
	return m.ServerID.ValueString() + "/" + m.UserID.ValueString()
}

// parseTimeout parses a communication_disabled_until value.
func parseTimeout(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

// timeoutActive reports whether a timeout ending at v is still in force. Null and
// unparsable values are not.
func timeoutActive(v types.String) bool {
	if v.IsNull() || v.IsUnknown() {
		return false
	}
	t, err := parseTimeout(v.ValueString())
	return err == nil && t.After(time.Now())
}

// timeoutValue is what to send for a configured timeout: the time while it is
// in the future, and null, meaning no timeout, once it has passed.
func timeoutValue(v types.String) any {
	if timeoutActive(v) {
		return v.ValueString()
	}
	return nil
}

func (r *memberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan memberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := discord.Payload{}
	putKnownString(p, "nick", plan.Nick)
	if !plan.CommunicationDisabledUntil.IsNull() {
		p["communication_disabled_until"] = timeoutValue(plan.CommunicationDisabledUntil)
	}
	if !r.modify(ctx, &plan, p, &resp.Diagnostics) {
		return
	}
	plan.ID = types.StringValue(plan.id())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// modify sends the payload, or only checks that the member exists when it is
// empty, and reports whether it succeeded.
func (r *memberResource) modify(ctx context.Context, m *memberModel, p discord.Payload, diags *diag.Diagnostics) bool {
	serverID, userID := m.ServerID.ValueString(), m.UserID.ValueString()
	var err error
	if len(p) == 0 {
		_, err = r.client.GetMember(ctx, serverID, userID)
	} else {
		_, err = r.client.ModifyMember(withAuditLogReason(ctx, m.AuditLogReason), serverID, userID, p)
	}
	if err != nil {
		apiError(diags, "update member", err)
		return false
	}
	return true
}

func (r *memberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state memberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	member, err := r.client.GetMember(ctx, state.ServerID.ValueString(), state.UserID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read member", err)
		return
	}
	// Import sets only server_id and user_id, so a null id means the
	// arguments have not been read yet.
	importing := state.ID.IsNull()
	if importing || !state.Nick.IsNull() {
		state.Nick = stringPtrValue(member.Nick)
	}
	current := stringPtrValue(member.CommunicationDisabledUntil)
	if !timeoutActive(current) {
		current = types.StringNull()
	}
	switch {
	case importing:
		state.CommunicationDisabledUntil = current
	case state.CommunicationDisabledUntil.IsNull():
	case !current.IsNull():
		if !sameTime(current, state.CommunicationDisabledUntil) {
			state.CommunicationDisabledUntil = current
		}
	case timeoutActive(state.CommunicationDisabledUntil):
		// The timeout was removed early, outside Terraform.
		state.CommunicationDisabledUntil = types.StringNull()
	}
	state.ID = types.StringValue(state.id())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// sameTime reports whether two timestamps are the same instant, which Discord
// may format differently from the configuration.
func sameTime(a, b types.String) bool {
	ta, errA := parseTimeout(a.ValueString())
	tb, errB := parseTimeout(b.ValueString())
	return errA == nil && errB == nil && ta.Equal(tb)
}

func (r *memberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan, state memberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := discord.Payload{}
	if !plan.Nick.Equal(state.Nick) {
		putString(p, "nick", plan.Nick)
	}
	until := plan.CommunicationDisabledUntil
	switch {
	case until.Equal(state.CommunicationDisabledUntil):
	case !until.IsNull():
		p["communication_disabled_until"] = timeoutValue(until)
	case timeoutActive(state.CommunicationDisabledUntil):
		p["communication_disabled_until"] = nil
	}
	if !r.modify(ctx, &plan, p, &resp.Diagnostics) {
		return
	}
	plan.ID = types.StringValue(plan.id())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *memberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state memberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := discord.Payload{}
	if !state.Nick.IsNull() {
		p["nick"] = nil
	}
	if timeoutActive(state.CommunicationDisabledUntil) {
		p["communication_disabled_until"] = nil
	}
	if len(p) == 0 {
		return
	}
	ctx = withAuditLogReason(ctx, state.AuditLogReason)
	_, err := r.client.ModifyMember(ctx, state.ServerID.ValueString(), state.UserID.ValueString(), p)
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "reset member", err)
	}
}

// timeoutValidator checks that communication_disabled_until is an RFC 3339
// timestamp no more than 28 days ahead. Past times are accepted: they mean
// the timeout has expired.
type timeoutValidator struct{}

func (v timeoutValidator) Description(context.Context) string {
	return "must be an RFC 3339 timestamp at most 28 days in the future"
}

func (v timeoutValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v timeoutValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	t, err := parseTimeout(req.ConfigValue.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid timestamp",
			fmt.Sprintf("Value must be an RFC 3339 timestamp such as 2030-01-01T00:00:00Z: %s.", err))
		return
	}
	if t.After(time.Now().Add(maxTimeout)) {
		resp.Diagnostics.AddAttributeError(req.Path, "Timeout too long",
			"Discord accepts timeouts of at most 28 days from now.")
	}
}
