package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ resource.ResourceWithConfigure      = &scheduledEventResource{}
	_ resource.ResourceWithImportState    = &scheduledEventResource{}
	_ resource.ResourceWithIdentity       = &scheduledEventResource{}
	_ resource.ResourceWithValidateConfig = &scheduledEventResource{}
)

var (
	scheduledEventEntityTypes    = enumMapping{"", "stage_instance", "voice", "external"}
	scheduledEventStatuses       = enumMapping{"", "scheduled", "active", "completed", "canceled"}
	recurrenceFrequencies        = enumMapping{"yearly", "monthly", "weekly", "daily"}
	recurrenceWeekdays           = enumMapping{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}
	recurrenceMonths             = enumMapping{"", "january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}
	coverImageRegexp             = regexp.MustCompile(`^data:image/(png|jpeg|webp);`)
	recurrenceDailyWeekdaySets   = [][]string{{"monday", "tuesday", "wednesday", "thursday", "friday"}, {"tuesday", "wednesday", "thursday", "friday", "saturday"}, {"sunday", "monday", "tuesday", "wednesday", "thursday"}, {"friday", "saturday"}, {"saturday", "sunday"}, {"sunday", "monday"}}
	recurrenceDailyWeekdaySetDoc = "Monday-Friday, Tuesday-Saturday, Sunday-Thursday, Friday-Saturday, Saturday-Sunday or Sunday-Monday"
)

func coverImageValidator() validator.String {
	return stringvalidator.RegexMatches(coverImageRegexp, "must be a PNG, JPEG or WebP image")
}

type scheduledEventResource struct {
	resourceIdentity
	client *discord.Client
}

type scheduledEventModel struct {
	ID                 types.String `tfsdk:"id"`
	ServerID           types.String `tfsdk:"server_id"`
	Name               types.String `tfsdk:"name"`
	Description        types.String `tfsdk:"description"`
	EntityType         types.String `tfsdk:"entity_type"`
	ChannelID          types.String `tfsdk:"channel_id"`
	Location           types.String `tfsdk:"location"`
	ScheduledStartTime types.String `tfsdk:"scheduled_start_time"`
	ScheduledEndTime   types.String `tfsdk:"scheduled_end_time"`
	RecurrenceRule     types.Object `tfsdk:"recurrence_rule"`
	Image              types.String `tfsdk:"image"`
	ImageWO            types.String `tfsdk:"image_wo"`
	ImageWOVersion     types.Int64  `tfsdk:"image_wo_version"`
	ImageHash          types.String `tfsdk:"image_hash"`
	Status             types.String `tfsdk:"status"`
	CreatorID          types.String `tfsdk:"creator_id"`
	AuditLogReason     types.String `tfsdk:"audit_log_reason"`
}

type recurrenceRuleModel struct {
	Frequency  types.String `tfsdk:"frequency"`
	Interval   types.Int64  `tfsdk:"interval"`
	ByWeekday  types.Set    `tfsdk:"by_weekday"`
	ByNWeekday types.List   `tfsdk:"by_n_weekday"`
	ByMonth    types.Set    `tfsdk:"by_month"`
	ByMonthDay types.Set    `tfsdk:"by_month_day"`
}

type nWeekdayModel struct {
	N   types.Int64  `tfsdk:"n"`
	Day types.String `tfsdk:"day"`
}

var (
	nWeekdayAttrTypes       = map[string]attr.Type{"n": types.Int64Type, "day": types.StringType}
	recurrenceRuleAttrTypes = map[string]attr.Type{
		"frequency":    types.StringType,
		"interval":     types.Int64Type,
		"by_weekday":   types.SetType{ElemType: types.StringType},
		"by_n_weekday": types.ListType{ElemType: types.ObjectType{AttrTypes: nWeekdayAttrTypes}},
		"by_month":     types.SetType{ElemType: types.StringType},
		"by_month_day": types.SetType{ElemType: types.Int64Type},
	}
)

func newScheduledEventResource() resource.Resource {
	return &scheduledEventResource{resourceIdentity: resourceIdentity{attrs: []identityAttribute{
		serverIdentity("server_id"),
		{name: "event_id", description: "ID of the scheduled event.", state: []string{"id"}},
	}}}
}

func (r *scheduledEventResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scheduled_event"
}

func (r *scheduledEventResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	image := writeOnlyImage("Cover image as a data URI (PNG, JPEG or WebP).", "image")
	image.Validators = append(image.Validators, coverImageValidator())
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a server scheduled event in a stage channel, a voice channel or an external " +
			"location. Events are visible to server members only.\n\n" +
			"Discord moves events through their `status` on its own: external events start and end at their scheduled " +
			"times, stage and voice events end a few minutes after everyone leaves the channel, and events that never " +
			"start are canceled a few hours after their start time. Starting and ending events is left to Discord and " +
			"the people hosting them. A completed or canceled event cannot be changed, so it is removed from state; " +
			"the next plan creates a new event, which needs a `scheduled_start_time` in the future.\n\n" +
			"The bot needs the Create Events permission, and Manage Events to change or delete events created by " +
			"others. Stage events also need Manage Channels, Mute Members and Move Members on the channel, and voice " +
			"events View Channel and Connect. A server can have at most 100 scheduled or active events.",
		Attributes: map[string]schema.Attribute{
			"audit_log_reason": auditLogReasonAttributeFor("creating and changing the event " +
				"(Discord records no reason for deleting it)"),
			"id":        idAttribute("Scheduled event ID."),
			"server_id": serverIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Event name (1-100 characters).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 100)},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Event description (1-1000 characters).",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 1000)},
			},
			"entity_type": schema.StringAttribute{
				MarkdownDescription: "Where the event takes place: " + scheduledEventEntityTypes.doc() + ". `stage_instance` " +
					"and `voice` events need `channel_id`; `external` events need `location` and `scheduled_end_time`. " +
					"Discord fails to turn an `external` event into a `stage_instance` event, so that change creates a new event.",
				Required:   true,
				Validators: []validator.String{scheduledEventEntityTypes.validator()},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = req.StateValue.ValueString() == "external" && req.PlanValue.ValueString() == "stage_instance"
					},
					"Changing an external event to a stage event creates a new event.",
					"Changing an external event to a stage event creates a new event.",
				)},
			},
			"channel_id": schema.StringAttribute{
				MarkdownDescription: "ID of the stage channel (`stage_instance`) or voice channel (`voice`) the event is " +
					"hosted in. Must not be set for `external` events.",
				Optional:   true,
				Validators: []validator.String{snowflakeValidator()},
			},
			"location": schema.StringAttribute{
				MarkdownDescription: "Location of an `external` event (1-100 characters), such as a URL or an address.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 100)},
			},
			"scheduled_start_time": schema.StringAttribute{
				MarkdownDescription: "When the event starts, as an RFC 3339 timestamp such as `2030-01-01T18:00:00Z`. " +
					"Discord requires it to be in the future when the event is created, and within five years from now. " +
					"Write it in UTC (`Z`) so that " +
					"an imported event plans no change.",
				Required:   true,
				Validators: []validator.String{rfc3339Validator{}},
			},
			"scheduled_end_time": schema.StringAttribute{
				MarkdownDescription: "When the event ends, as an RFC 3339 timestamp after `scheduled_start_time` and " +
					"within five years from now. Required for `external` events.",
				Optional:   true,
				Validators: []validator.String{rfc3339Validator{}},
			},
			"recurrence_rule": recurrenceRuleAttribute(),
			"image": schema.StringAttribute{
				MarkdownDescription: "Cover image as a data URI (PNG, JPEG or WebP), e.g. " +
					"`\"data:image/png;base64,${filebase64(\"cover.png\")}\"`. Stored in state; prefer `image_wo` on " +
					"Terraform 1.11 or later. Removing it removes the cover image.",
				Optional: true,
				Validators: []validator.String{
					dataURIValidator(),
					coverImageValidator(),
					stringvalidator.ConflictsWith(path.MatchRoot("image_wo")),
				},
			},
			"image_wo": image,
			"image_wo_version": writeOnlyVersion("image",
				"Setting or changing it uploads `image_wo`; removing it removes the cover image unless `image` is set."),
			"image_hash": schema.StringAttribute{
				MarkdownDescription: "Hash of the current cover image. A change made outside Terraform makes the next plan " +
					"upload the configured image again.",
				Computed: true,
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "Current status: `scheduled` or `active`. Completed and canceled events are " +
					"removed from state.",
				Computed: true,
			},
			"creator_id": schema.StringAttribute{
				MarkdownDescription: "ID of the user that created the event.",
				Computed:            true,
			},
		},
	}
}

func recurrenceRuleAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "Makes the event repeat, starting at `scheduled_start_time`. Discord supports a subset " +
			"of iCalendar rules: `by_weekday` only with `daily` or `weekly`, `by_n_weekday` only with `monthly`, and " +
			"`by_month` with `by_month_day` only with `yearly`. Recurrences cannot end on a date or after a count.",
		Optional: true,
		Attributes: map[string]schema.Attribute{
			"frequency": schema.StringAttribute{
				MarkdownDescription: "How often the event repeats: " + recurrenceFrequencies.doc() + ".",
				Required:            true,
				Validators:          []validator.String{recurrenceFrequencies.validator()},
			},
			"interval": schema.Int64Attribute{
				MarkdownDescription: "Number of `frequency` periods between occurrences. Only `weekly` events accept " +
					"`2`, for every other week. Defaults to `1`.",
				Optional:   true,
				Computed:   true,
				Default:    int64default.StaticInt64(1),
				Validators: []validator.Int64{int64validator.Between(1, 2)},
			},
			"by_weekday": schema.SetAttribute{
				MarkdownDescription: "Days of the week the event occurs on: " + recurrenceWeekdays.doc() + ". " +
					"`weekly` events take one day; `daily` events take one of these sets: " +
					recurrenceDailyWeekdaySetDoc + ".",
				ElementType: types.StringType,
				Optional:    true,
				Validators:  []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(recurrenceWeekdays.validator())},
			},
			"by_n_weekday": schema.ListNestedAttribute{
				MarkdownDescription: "The day of a given week of the month a `monthly` event occurs on. Exactly one entry.",
				Optional:            true,
				Validators:          []validator.List{listvalidator.SizeBetween(1, 1)},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"n": schema.Int64Attribute{
						MarkdownDescription: "Week of the month, `1` to `5`.",
						Required:            true,
						Validators:          []validator.Int64{int64validator.Between(1, 5)},
					},
					"day": schema.StringAttribute{
						MarkdownDescription: "Day of the week: " + recurrenceWeekdays.doc() + ".",
						Required:            true,
						Validators:          []validator.String{recurrenceWeekdays.validator()},
					},
				}},
			},
			"by_month": schema.SetAttribute{
				MarkdownDescription: "Month a `yearly` event occurs in: " + recurrenceMonths.doc() + ". Exactly one " +
					"month; requires `by_month_day`.",
				ElementType: types.StringType,
				Optional:    true,
				Validators:  []validator.Set{setvalidator.SizeBetween(1, 1), setvalidator.ValueStringsAre(recurrenceMonths.validator())},
			},
			"by_month_day": schema.SetAttribute{
				MarkdownDescription: "Day of the month a `yearly` event occurs on, `1` to `31`. Exactly one day; " +
					"requires `by_month`.",
				ElementType: types.Int64Type,
				Optional:    true,
				Validators:  []validator.Set{setvalidator.SizeBetween(1, 1), setvalidator.ValueInt64sAre(int64validator.Between(1, 31))},
			},
		},
	}
}

func (r *scheduledEventResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromResource(req, resp)
}

func (r *scheduledEventResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m scheduledEventModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	invalid := func(name, msg string) {
		resp.Diagnostics.AddAttributeError(path.Root(name), "Invalid argument", msg)
	}
	if !m.EntityType.IsUnknown() && !m.EntityType.IsNull() {
		if m.EntityType.ValueString() == "external" {
			if !m.ChannelID.IsNull() {
				invalid("channel_id", "channel_id cannot be set on an external event.")
			}
			if m.Location.IsNull() {
				invalid("location", "location is required for an external event.")
			}
			if m.ScheduledEndTime.IsNull() {
				invalid("scheduled_end_time", "scheduled_end_time is required for an external event.")
			}
		} else {
			if m.ChannelID.IsNull() {
				invalid("channel_id", "channel_id is required for a "+m.EntityType.ValueString()+" event.")
			}
			if !m.Location.IsNull() {
				invalid("location", "location can only be set on an external event.")
			}
		}
	}
	if isSet(m.ScheduledStartTime) && isSet(m.ScheduledEndTime) {
		start, errStart := time.Parse(time.RFC3339, m.ScheduledStartTime.ValueString())
		end, errEnd := time.Parse(time.RFC3339, m.ScheduledEndTime.ValueString())
		if errStart == nil && errEnd == nil && !end.After(start) {
			invalid("scheduled_end_time", "scheduled_end_time must be after scheduled_start_time.")
		}
	}
	// Discord rejects events that start or end more than five years ahead.
	limit := time.Now().AddDate(5, 0, 0)
	for _, a := range []struct {
		name string
		v    types.String
	}{{"scheduled_start_time", m.ScheduledStartTime}, {"scheduled_end_time", m.ScheduledEndTime}} {
		if !isSet(a.v) {
			continue
		}
		if t, err := time.Parse(time.RFC3339, a.v.ValueString()); err == nil && t.After(limit) {
			invalid(a.name, a.name+" must be within five years from now.")
		}
	}
	if isSet(m.RecurrenceRule) {
		var rule recurrenceRuleModel
		resp.Diagnostics.Append(m.RecurrenceRule.As(ctx, &rule, basetypes.ObjectAsOptions{})...)
		if !resp.Diagnostics.HasError() {
			rule.validate(ctx, &resp.Diagnostics)
		}
	}
}

// validate checks Discord's documented limits on recurrence rules, which
// depend on the frequency.
func (rule *recurrenceRuleModel) validate(ctx context.Context, diags *diag.Diagnostics) {
	if rule.Frequency.IsUnknown() {
		return
	}
	freq := rule.Frequency.ValueString()
	invalid := func(name, msg string) {
		diags.AddAttributeError(path.Root("recurrence_rule").AtName(name), "Invalid recurrence rule", msg)
	}
	if isSet(rule.Interval) && rule.Interval.ValueInt64() != 1 && freq != "weekly" {
		invalid("interval", "interval can only be 2 when frequency is weekly.")
	}
	if !rule.ByWeekday.IsNull() {
		switch freq {
		case "weekly":
			if isSet(rule.ByWeekday) && len(rule.ByWeekday.Elements()) != 1 {
				invalid("by_weekday", "A weekly event occurs on exactly one day of the week; use frequency = \"daily\" for several days.")
			}
		case "daily":
			if days, ok := knownStrings(ctx, rule.ByWeekday); ok && !slices.ContainsFunc(recurrenceDailyWeekdaySets, func(set []string) bool {
				return len(set) == len(days) && !slices.ContainsFunc(days, func(d string) bool { return !slices.Contains(set, d) })
			}) {
				invalid("by_weekday", "A daily event can only occur on "+recurrenceDailyWeekdaySetDoc+".")
			}
		default:
			invalid("by_weekday", "by_weekday can only be set when frequency is daily or weekly.")
		}
	}
	if !rule.ByNWeekday.IsNull() && freq != "monthly" {
		invalid("by_n_weekday", "by_n_weekday can only be set when frequency is monthly.")
	}
	byMonth, byMonthDay := !rule.ByMonth.IsNull(), !rule.ByMonthDay.IsNull()
	switch {
	case (byMonth || byMonthDay) && freq != "yearly":
		invalid("by_month", "by_month and by_month_day can only be set when frequency is yearly.")
	case byMonth != byMonthDay:
		invalid("by_month", "by_month and by_month_day must be set together.")
	}
}

// knownStrings returns the elements of a set when all of them are known.
func knownStrings(ctx context.Context, set types.Set) ([]string, bool) {
	if !isSet(set) {
		return nil, false
	}
	for _, e := range set.Elements() {
		if e.IsUnknown() {
			return nil, false
		}
	}
	var out []string
	return out, !set.ElementsAs(ctx, &out, false).HasError()
}

// rfc3339Validator checks that a string is an RFC 3339 timestamp.
type rfc3339Validator struct{}

func (rfc3339Validator) Description(context.Context) string {
	return "must be an RFC 3339 timestamp"
}

func (v rfc3339Validator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (rfc3339Validator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !isSet(req.ConfigValue) {
		return
	}
	if _, err := time.Parse(time.RFC3339, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid timestamp",
			fmt.Sprintf("Value must be an RFC 3339 timestamp such as 2030-01-01T18:00:00Z, got %q.", req.ConfigValue.ValueString()))
	}
}

// timestampValue returns prior when it names the same instant as Discord's
// timestamp, so that writing "Z" where Discord returns "+00:00" is not drift.
// Otherwise it returns Discord's timestamp in UTC, the form an imported event
// gets.
func timestampValue(prior types.String, current *string) types.String {
	if current == nil {
		return types.StringNull()
	}
	c, err := time.Parse(time.RFC3339, *current)
	if err != nil {
		return types.StringValue(*current)
	}
	if isSet(prior) {
		if p, err := time.Parse(time.RFC3339, prior.ValueString()); err == nil && p.Equal(c) {
			return prior
		}
	}
	return types.StringValue(c.UTC().Format(time.RFC3339Nano))
}

// payload returns the request body for the event's configurable fields.
// Image is left to the caller.
func (m *scheduledEventModel) payload(ctx context.Context, diags *diag.Diagnostics) discord.Payload {
	p := discord.Payload{"privacy_level": discord.PrivacyLevelGuildOnly}
	putString(p, "name", m.Name)
	putString(p, "description", m.Description)
	scheduledEventEntityTypes.put(p, "entity_type", m.EntityType)
	if m.EntityType.ValueString() == "external" {
		p["channel_id"] = nil
		if isSet(m.Location) {
			p["entity_metadata"] = discord.Payload{"location": m.Location.ValueString()}
		}
	} else {
		putString(p, "channel_id", m.ChannelID)
	}
	putString(p, "scheduled_start_time", m.ScheduledStartTime)
	putString(p, "scheduled_end_time", m.ScheduledEndTime)
	p["recurrence_rule"] = m.recurrenceRulePayload(ctx, diags)
	return p
}

// recurrenceRulePayload returns the rule as Discord expects it, or nil. The
// recurrence starts with the event.
func (m *scheduledEventModel) recurrenceRulePayload(ctx context.Context, diags *diag.Diagnostics) any {
	if m.RecurrenceRule.IsNull() || m.RecurrenceRule.IsUnknown() {
		return nil
	}
	var rule recurrenceRuleModel
	diags.Append(m.RecurrenceRule.As(ctx, &rule, basetypes.ObjectAsOptions{})...)
	freq, _ := recurrenceFrequencies.value(rule.Frequency.ValueString())
	p := discord.Payload{
		"start":     m.ScheduledStartTime.ValueString(),
		"frequency": freq,
		"interval":  rule.Interval.ValueInt64(),
	}
	if !rule.ByWeekday.IsNull() {
		p["by_weekday"] = enumValues(ctx, recurrenceWeekdays, rule.ByWeekday, diags)
	}
	if !rule.ByNWeekday.IsNull() {
		var entries []nWeekdayModel
		diags.Append(rule.ByNWeekday.ElementsAs(ctx, &entries, false)...)
		out := make([]discord.NWeekday, 0, len(entries))
		for _, e := range entries {
			day, _ := recurrenceWeekdays.value(e.Day.ValueString())
			out = append(out, discord.NWeekday{N: e.N.ValueInt64(), Day: day})
		}
		p["by_n_weekday"] = out
	}
	if !rule.ByMonth.IsNull() {
		p["by_month"] = enumValues(ctx, recurrenceMonths, rule.ByMonth, diags)
	}
	if !rule.ByMonthDay.IsNull() {
		var days []int64
		diags.Append(rule.ByMonthDay.ElementsAs(ctx, &days, false)...)
		slices.Sort(days)
		p["by_month_day"] = days
	}
	return p
}

// enumValues converts a set of enum names to Discord's values, sorted so
// that payloads compare equal regardless of set order.
func enumValues(ctx context.Context, m enumMapping, set types.Set, diags *diag.Diagnostics) []int64 {
	var names []string
	diags.Append(set.ElementsAs(ctx, &names, false)...)
	out := make([]int64, 0, len(names))
	for _, n := range names {
		if v, ok := m.value(n); ok {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}

func enumSetValue(ctx context.Context, m enumMapping, values []int64, diags *diag.Diagnostics) types.Set {
	if len(values) == 0 {
		return types.SetNull(types.StringType)
	}
	names := make([]string, 0, len(values))
	for _, v := range values {
		names = append(names, m.name(v).ValueString())
	}
	return stringSetValue(ctx, names, diags)
}

func recurrenceRuleValue(ctx context.Context, rule *discord.RecurrenceRule, diags *diag.Diagnostics) types.Object {
	if rule == nil {
		return types.ObjectNull(recurrenceRuleAttrTypes)
	}
	m := recurrenceRuleModel{
		Frequency:  recurrenceFrequencies.name(rule.Frequency),
		Interval:   types.Int64Value(rule.Interval),
		ByWeekday:  enumSetValue(ctx, recurrenceWeekdays, rule.ByWeekday, diags),
		ByNWeekday: types.ListNull(types.ObjectType{AttrTypes: nWeekdayAttrTypes}),
		ByMonth:    enumSetValue(ctx, recurrenceMonths, rule.ByMonth, diags),
		ByMonthDay: types.SetNull(types.Int64Type),
	}
	if len(rule.ByNWeekday) > 0 {
		entries := make([]nWeekdayModel, 0, len(rule.ByNWeekday))
		for _, e := range rule.ByNWeekday {
			entries = append(entries, nWeekdayModel{N: types.Int64Value(e.N), Day: recurrenceWeekdays.name(e.Day)})
		}
		list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: nWeekdayAttrTypes}, entries)
		diags.Append(d...)
		m.ByNWeekday = list
	}
	if len(rule.ByMonthDay) > 0 {
		set, d := types.SetValueFrom(ctx, types.Int64Type, rule.ByMonthDay)
		diags.Append(d...)
		m.ByMonthDay = set
	}
	obj, d := types.ObjectValueFrom(ctx, recurrenceRuleAttrTypes, m)
	diags.Append(d...)
	return obj
}

func (m *scheduledEventModel) apply(ctx context.Context, e *discord.ScheduledEvent, diags *diag.Diagnostics) {
	m.ID = types.StringValue(e.ID)
	m.ServerID = types.StringValue(e.GuildID)
	m.Name = types.StringValue(e.Name)
	m.Description = stringPtrValue(e.Description)
	m.EntityType = scheduledEventEntityTypes.name(e.EntityType)
	m.ChannelID = stringPtrValue(e.ChannelID)
	m.Location = types.StringNull()
	if e.EntityMetadata != nil {
		m.Location = stringPtrValue(e.EntityMetadata.Location)
	}
	m.ScheduledStartTime = timestampValue(m.ScheduledStartTime, &e.ScheduledStartTime)
	m.ScheduledEndTime = timestampValue(m.ScheduledEndTime, e.ScheduledEndTime)
	m.RecurrenceRule = recurrenceRuleValue(ctx, e.RecurrenceRule, diags)
	m.ImageHash = stringPtrValue(e.Image)
	m.Status = scheduledEventStatuses.name(e.Status)
	m.CreatorID = stringPtrValue(e.CreatorID)
}

// scheduledEventEnded reports whether Discord has completed or canceled the event, after
// which it can no longer be changed.
func scheduledEventEnded(e *discord.ScheduledEvent) bool {
	return e.Status == discord.ScheduledEventStatusCompleted || e.Status == discord.ScheduledEventStatusCanceled
}

func (r *scheduledEventResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	var plan scheduledEventModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	p := plan.payload(ctx, &resp.Diagnostics)
	putKnownString(p, "image", plan.Image)
	if !plan.ImageWOVersion.IsNull() {
		putKnownString(p, "image", writeOnlyString(ctx, req.Config, "image_wo", &resp.Diagnostics))
	}
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.client.CreateScheduledEvent(ctx, plan.ServerID.ValueString(), p)
	if err != nil {
		apiError(&resp.Diagnostics, "create scheduled event", err)
		return
	}
	plan.apply(ctx, e, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *scheduledEventResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State, &req.State)
	var state scheduledEventModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.client.GetScheduledEvent(ctx, state.ServerID.ValueString(), state.ID.ValueString())
	if discord.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read scheduled event", err)
		return
	}
	if scheduledEventEnded(e) {
		resp.State.RemoveResource(ctx)
		return
	}
	clearImageOnDrift(state.ImageHash, e.Image, &state.Image, &state.ImageWOVersion)
	state.apply(ctx, e, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *scheduledEventResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	defer r.setIdentity(ctx, resp.Identity, &resp.Diagnostics, &resp.State)
	if updateAuditLogReasonOnly(ctx, req, resp) {
		return
	}
	var plan, state scheduledEventModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx = withAuditLogReason(ctx, plan.AuditLogReason)
	desired, current := plan.payload(ctx, &resp.Diagnostics), state.payload(ctx, &resp.Diagnostics)
	// image_wo is compared through its version below.
	if plan.ImageWOVersion.IsNull() {
		putString(desired, "image", plan.Image)
	}
	if state.ImageWOVersion.IsNull() {
		putString(current, "image", state.Image)
	}
	payload := diffPayload(desired, current)
	if _, ok := payload["entity_type"]; ok {
		// Discord checks the fields an entity type requires on every change
		// of entity type, so they are sent even when unchanged.
		for _, k := range []string{"channel_id", "entity_metadata", "scheduled_end_time"} {
			if v, ok := desired[k]; ok {
				payload[k] = v
			}
		}
	}
	if writeOnlyChanged(plan.ImageWOVersion, state.ImageWOVersion) {
		putKnownString(payload, "image", writeOnlyString(ctx, req.Config, "image_wo", &resp.Diagnostics))
	}
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.client.ModifyScheduledEvent(ctx, state.ServerID.ValueString(), state.ID.ValueString(), payload)
	if err != nil {
		apiError(&resp.Diagnostics, "update scheduled event", err)
		return
	}
	plan.apply(ctx, e, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *scheduledEventResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state scheduledEventModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteScheduledEvent(ctx, state.ServerID.ValueString(), state.ID.ValueString())
	if err != nil && !discord.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete scheduled event", err)
	}
}
