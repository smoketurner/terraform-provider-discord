package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	snowflakeRegexp   = regexp.MustCompile(`^[0-9]{1,20}$`)
	permissionsRegexp = regexp.MustCompile(`^(0|[1-9][0-9]{0,19})$`)
)

func snowflakeValidator() validator.String {
	return stringvalidator.RegexMatches(snowflakeRegexp, "must be a Discord snowflake ID")
}

func permissionsValidator() validator.String {
	return stringvalidator.RegexMatches(permissionsRegexp,
		"must be a decimal permission bitfield, e.g. from provider::discord::permissions()")
}

// idAttribute is the computed "id" attribute every resource exposes.
func idAttribute(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// serverIDAttribute is a required server ID that forces replacement.
func serverIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "ID of the server (guild).",
		Required:            true,
		Validators:          []validator.String{snowflakeValidator()},
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

func clientFromResource(req resource.ConfigureRequest, resp *resource.ConfigureResponse) *discord.Client {
	if req.ProviderData == nil {
		return nil
	}
	c, ok := req.ProviderData.(*discord.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *discord.Client, got %T", req.ProviderData))
	}
	return c
}

func clientFromDataSource(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) *discord.Client {
	if req.ProviderData == nil {
		return nil
	}
	c, ok := req.ProviderData.(*discord.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *discord.Client, got %T", req.ProviderData))
	}
	return c
}

func apiError(diags *diag.Diagnostics, action string, err error) {
	diags.AddError("Discord API error", fmt.Sprintf("Unable to %s: %s", action, err))
}

// splitID splits a composite import ID of n parts joined by "/".
func splitID(id string, n int, format string) ([]string, error) {
	parts := strings.Split(id, "/")
	if len(parts) != n {
		return nil, fmt.Errorf("expected import ID in the form %q, got %q", format, id)
	}
	for _, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("expected import ID in the form %q, got %q", format, id)
		}
	}
	return parts, nil
}

// diffPayload returns the keys of desired whose values differ from current.
// Values are compared by their JSON encoding so numeric types and nil
// pointers compare as Discord would see them.
func diffPayload(desired, current discord.Payload) discord.Payload {
	out := discord.Payload{}
	for k, v := range desired {
		cur, ok := current[k]
		if !ok || !jsonEqual(v, cur) {
			out[k] = v
		}
	}
	return out
}

func jsonEqual(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return reflect.DeepEqual(a, b)
	}
	return string(ja) == string(jb)
}

// The put* helpers add a value to a payload when it is known. Null values are
// sent as JSON null so Discord clears the field; unknown values are omitted.

func putString(p discord.Payload, key string, v types.String) {
	switch {
	case v.IsUnknown():
	case v.IsNull():
		p[key] = nil
	default:
		p[key] = v.ValueString()
	}
}

func putInt(p discord.Payload, key string, v types.Int64) {
	switch {
	case v.IsUnknown():
	case v.IsNull():
		p[key] = nil
	default:
		p[key] = v.ValueInt64()
	}
}

func putBool(p discord.Payload, key string, v types.Bool) {
	if !v.IsUnknown() && !v.IsNull() {
		p[key] = v.ValueBool()
	}
}

// putKnownString adds a string only when set, leaving null values unmanaged.
func putKnownString(p discord.Payload, key string, v types.String) {
	if !v.IsUnknown() && !v.IsNull() {
		p[key] = v.ValueString()
	}
}

func putKnownInt(p discord.Payload, key string, v types.Int64) {
	if !v.IsUnknown() && !v.IsNull() {
		p[key] = v.ValueInt64()
	}
}

func stringPtrValue(s *string) types.String {
	if s == nil || *s == "" {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

func stringSetValue(ctx context.Context, values []string, diags *diag.Diagnostics) types.Set {
	if values == nil {
		values = []string{}
	}
	set, d := types.SetValueFrom(ctx, types.StringType, values)
	diags.Append(d...)
	return set
}

func stringListValue(ctx context.Context, values []string, diags *diag.Diagnostics) types.List {
	if values == nil {
		values = []string{}
	}
	list, d := types.ListValueFrom(ctx, types.StringType, values)
	diags.Append(d...)
	return list
}

// enumMapping converts between Terraform string values and Discord integers.
type enumMapping []string

func (m enumMapping) name(v int64) types.String {
	if v >= 0 && int(v) < len(m) && m[v] != "" {
		return types.StringValue(m[v])
	}
	return types.StringValue(fmt.Sprintf("unknown_%d", v))
}

func (m enumMapping) value(name string) (int64, bool) {
	for i, n := range m {
		if n != "" && n == name {
			return int64(i), true
		}
	}
	return 0, false
}

func (m enumMapping) validator() validator.String {
	var names []string
	for _, n := range m {
		if n != "" {
			names = append(names, n)
		}
	}
	return stringvalidator.OneOf(names...)
}

func (m enumMapping) put(p discord.Payload, key string, v types.String) {
	if v.IsUnknown() || v.IsNull() {
		return
	}
	if n, ok := m.value(v.ValueString()); ok {
		p[key] = n
	}
}

func (m enumMapping) doc() string {
	var names []string
	for _, n := range m {
		if n != "" {
			names = append(names, "`"+n+"`")
		}
	}
	return strings.Join(names, ", ")
}

// notContainsValidator rejects strings containing any of the given words,
// case-insensitively.
type notContainsValidator []string

func (v notContainsValidator) Description(context.Context) string {
	return "must not contain " + strings.Join(v, " or ")
}

func (v notContainsValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v notContainsValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	lower := strings.ToLower(req.ConfigValue.ValueString())
	for _, word := range v {
		if strings.Contains(lower, word) {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid value", fmt.Sprintf("Value must not contain %q.", word))
		}
	}
}
