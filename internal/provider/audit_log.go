package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

const auditLogReasonAttr = "audit_log_reason"

// Discord documents a 512-character limit; it is checked on the reason as
// written, before URL encoding.
func auditLogReasonValidator() validator.String {
	return stringvalidator.UTF8LengthBetween(1, discord.MaxAuditLogReasonLength)
}

// auditLogReasonAttribute is the per-resource override of the provider's
// audit_log_reason. It is kept in state so that destroying the resource uses
// the reason from its last apply.
func auditLogReasonAttribute() schema.StringAttribute {
	return auditLogReasonAttributeFor("changes this resource makes")
}

func auditLogReasonAttributeFor(scope string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Reason recorded in the server's audit log for " + scope + ", overriding the provider's " +
			"`audit_log_reason`. Up to 512 characters. Changing only this argument updates state without calling Discord.",
		Optional:   true,
		Validators: []validator.String{auditLogReasonValidator()},
	}
}

// withAuditLogReason returns a context carrying the resource's reason, if set,
// for the client to send in place of the provider's.
func withAuditLogReason(ctx context.Context, reason types.String) context.Context {
	if reason.IsNull() || reason.IsUnknown() {
		return ctx
	}
	return discord.WithAuditLogReason(ctx, reason.ValueString())
}

// updateAuditLogReasonOnly handles an update that changes nothing but
// audit_log_reason by saving the new reason to state without calling Discord.
// It reports whether it handled the update; resources call it first in
// Update.
func updateAuditLogReasonOnly(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) bool {
	if !onlyAuditLogReasonChanged(req) {
		return false
	}
	var reason types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root(auditLogReasonAttr), &reason)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(auditLogReasonAttr), reason)...)
	return true
}

// onlyAuditLogReasonChanged compares the plan with the prior state attribute
// by attribute. A computed attribute the plan marks unknown because the
// resource has a change counts as unchanged when it is not configured; an
// unknown configured value is a real change.
func onlyAuditLogReasonChanged(req resource.UpdateRequest) bool {
	var plan, state, config map[string]tftypes.Value
	if req.Plan.Raw.As(&plan) != nil || req.State.Raw.As(&state) != nil || req.Config.Raw.As(&config) != nil {
		return false
	}
	for name, planned := range plan {
		if name == auditLogReasonAttr || planned.Equal(state[name]) {
			continue
		}
		if !planned.IsFullyKnown() && config[name].IsNull() {
			continue
		}
		return false
	}
	return true
}
