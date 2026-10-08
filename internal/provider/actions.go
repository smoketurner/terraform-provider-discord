package provider

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// actionClient gives an action the provider's client. Actions embed it.
type actionClient struct {
	client *discord.Client
}

func (a *actionClient) Configure(_ context.Context, req action.ConfigureRequest, resp *action.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*discord.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *discord.Client, got %T", req.ProviderData))
		return
	}
	a.client = c
}

func actionID(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Required:            true,
		Validators:          []validator.String{snowflakeValidator()},
	}
}

// actionIDSet is a required set of between minItems and maxItems IDs.
func actionIDSet(desc string, minItems, maxItems int, validators ...validator.Set) schema.SetAttribute {
	return schema.SetAttribute{
		MarkdownDescription: desc,
		ElementType:         types.StringType,
		Required:            true,
		Validators: append([]validator.Set{
			setvalidator.SizeBetween(minItems, maxItems),
			setvalidator.ValueStringsAre(snowflakeValidator()),
		}, validators...),
	}
}

// actionAuditLogReason is the reason an action records in the audit log in
// place of the provider's audit_log_reason.
func actionAuditLogReason() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Reason recorded in the server's audit log for this action, overriding the provider's " +
			"`audit_log_reason`. Up to 512 characters.",
		Optional:   true,
		Validators: []validator.String{auditLogReasonValidator()},
	}
}

// progress reports what an action did, which Terraform shows while the
// action runs.
func progress(resp *action.InvokeResponse, format string, args ...any) {
	resp.SendProgress(action.InvokeProgressEvent{Message: fmt.Sprintf(format, args...)})
}

// snowflakeTime returns when a snowflake ID was created.
func snowflakeTime(id string) (time.Time, bool) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(n>>22 + discordEpoch), true
}
