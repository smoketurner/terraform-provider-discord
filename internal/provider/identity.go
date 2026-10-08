package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// resourceIdentity gives a resource an identity schema mirroring its string
// import ID, and imports by either one. Resources embed it, which provides
// IdentitySchema and ImportState, and defer setIdentity at the top of Create,
// Read and Update so that every path, early returns included, records the
// identity of whatever ends up in state.
type resourceIdentity struct {
	// attrs are the parts of the import ID, in order.
	attrs []identityAttribute
}

type identityAttribute struct {
	name        string
	description string
	// state lists the state attributes that hold the value. The identity is
	// read from the first.
	state []string
}

func (ri resourceIdentity) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	attrs := make(map[string]identityschema.Attribute, len(ri.attrs))
	for _, a := range ri.attrs {
		attrs[a.name] = identityschema.StringAttribute{Description: a.description, RequiredForImport: true}
	}
	resp.IdentitySchema = identityschema.Schema{Attributes: attrs}
}

func (ri resourceIdentity) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := ri.importParts(ctx, req, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	for i, a := range ri.attrs {
		for _, s := range a.state {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(s), parts[i])...)
		}
	}
}

// identityNames returns the names of the identity attributes, in import ID
// order.
func (ri resourceIdentity) identityNames() []string {
	names := make([]string, len(ri.attrs))
	for i, a := range ri.attrs {
		names[i] = a.name
	}
	return names
}

// importParts returns the import ID parts from the string ID, or from the
// identity when importing by identity.
func (ri resourceIdentity) importParts(ctx context.Context, req resource.ImportStateRequest, diags *diag.Diagnostics) []string {
	names := ri.identityNames()
	if req.ID != "" {
		parts, err := splitID(req.ID, len(names), strings.Join(names, "/"))
		if err != nil {
			diags.AddError("Invalid import ID", err.Error())
		}
		return parts
	}
	parts := make([]string, len(names))
	for i, name := range names {
		var v types.String
		diags.Append(req.Identity.GetAttribute(ctx, path.Root(name), &v)...)
		if diags.HasError() {
			return nil
		}
		if v.ValueString() == "" {
			diags.AddAttributeError(path.Root(name), "Invalid import identity", fmt.Sprintf("Identity attribute %q must not be empty.", name))
			return nil
		}
		parts[i] = v.ValueString()
	}
	return parts
}

// setIdentity copies the identity attributes from the first of states that
// holds the resource, and does nothing when none does, such as after a failed
// create. Read passes its prior state too: the framework requires an identity
// even when Read removes the resource, and Terraform before 1.12 never sends
// the prior identity to fall back on.
func (ri resourceIdentity) setIdentity(ctx context.Context, identity *tfsdk.ResourceIdentity, diags *diag.Diagnostics, states ...*tfsdk.State) {
	for _, state := range states {
		if state.Raw.IsNull() {
			continue
		}
		for _, a := range ri.attrs {
			var v types.String
			diags.Append(state.GetAttribute(ctx, path.Root(a.state[0]), &v)...)
			diags.Append(identity.SetAttribute(ctx, path.Root(a.name), v)...)
		}
		return
	}
}

// Identity attributes shared by several resources.

func serverIdentity(state ...string) identityAttribute {
	return identityAttribute{name: "server_id", description: "ID of the server (guild).", state: state}
}

func channelIdentity(state ...string) identityAttribute {
	return identityAttribute{name: "channel_id", description: "ID of the channel.", state: state}
}
