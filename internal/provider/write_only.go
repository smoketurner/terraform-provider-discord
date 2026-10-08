package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Uploaded files have write-only variants named <name>_wo (Terraform 1.11+).
// Their values never reach plan or state, so Terraform cannot diff them; the
// paired <name>_wo_version attribute tells the provider when to send the
// value again.

// writeOnlyImage is the write-only variant of the stored image argument name.
func writeOnlyImage(desc, name string) schema.StringAttribute {
	return writeOnlyUpload(desc, name, dataURIValidator())
}

// writeOnlyUpload is the write-only variant of the stored upload argument
// name, whose value v validates.
func writeOnlyUpload(desc, name string, v validator.String) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc + " Write-only: the value is never stored in plan or state. Requires Terraform 1.11 " +
			"or later and `" + name + "_wo_version`. Conflicts with `" + name + "`.",
		Optional:  true,
		WriteOnly: true,
		Validators: []validator.String{
			v,
			stringvalidator.ConflictsWith(path.MatchRoot(name)),
			stringvalidator.AlsoRequires(path.MatchRoot(name + "_wo_version")),
		},
	}
}

// writeOnlyVersion is the version attribute that triggers sending the
// write-only image name_wo.
func writeOnlyVersion(name, effect string, modifiers ...planmodifier.Int64) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: "Version of `" + name + "_wo`. " + effect,
		Optional:            true,
		Validators:          []validator.Int64{int64validator.AlsoRequires(path.MatchRoot(name + "_wo"))},
		PlanModifiers:       modifiers,
	}
}

// replacesUpload reports whether a change to a file Discord cannot change in
// place, such as an emoji image, needs a new resource. Imported resources have
// no file in state, and switching between <name> and <name>_wo keeps the same
// file, so adopting a configured file needs no new upload.
func replacesUpload(state, plan attr.Value) bool {
	return !state.IsNull() && !plan.IsNull()
}

// replaceOnUpload replaces the resource when the stored file changes.
func replaceOnUpload(desc string) planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = replacesUpload(req.StateValue, req.PlanValue)
		}, desc, desc)
}

// replaceOnUploadVersion replaces the resource when the version of the
// write-only file changes.
func replaceOnUploadVersion(desc string) planmodifier.Int64 {
	return int64planmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.Int64Request, resp *int64planmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = replacesUpload(req.StateValue, req.PlanValue)
		}, desc, desc)
}

// writeOnlyString reads a write-only attribute from configuration, the only
// place its value is available.
func writeOnlyString(ctx context.Context, config tfsdk.Config, name string, diags *diag.Diagnostics) types.String {
	var v types.String
	diags.Append(config.GetAttribute(ctx, path.Root(name), &v)...)
	return v
}

// writeOnlyChanged reports whether a write-only value must be sent because its
// version was set or changed.
func writeOnlyChanged(plan, state types.Int64) bool {
	return !plan.IsNull() && !plan.Equal(state)
}

// clearImageOnDrift handles an image changed outside Terraform. Discord only
// returns a hash of the image, so when the hash no longer matches the one
// recorded at the last apply, the managed image arguments are cleared from
// state and the next plan uploads the configured image again.
func clearImageOnDrift(recorded types.String, current *string, image *types.String, version *types.Int64) {
	if !stringPtrValue(current).Equal(recorded) {
		*image = types.StringNull()
		*version = types.Int64Null()
	}
}
