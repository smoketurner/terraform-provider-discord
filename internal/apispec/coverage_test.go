package apispec

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/function"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/smoketurner/terraform-provider-discord/internal/provider"
)

const manifestPath = "../../coverage.yaml"

// pinnedSpec loads the spec named by DISCORD_API_SPEC. The spec is not
// vendored; `make api-coverage` downloads the pinned commit and runs these
// tests, and so does the API coverage CI job.
func pinnedSpec(t *testing.T) *Spec {
	t.Helper()
	path := os.Getenv("DISCORD_API_SPEC")
	if path == "" {
		t.Skip("DISCORD_API_SPEC is not set; run make api-coverage")
	}
	b, err := os.ReadFile(path) //nolint:gosec // The developer or CI job chooses the spec file.
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySHA256(b, PinnedSHA256); err != nil {
		t.Fatalf("%s is not the pinned spec: %v", path, err)
	}
	s, err := ParseSpec(b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func loadManifest(t *testing.T) *Manifest {
	t.Helper()
	m, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManifestMapsEveryOperation(t *testing.T) {
	s := pinnedSpec(t)
	if err := loadManifest(t).Check(s); err != nil {
		t.Errorf("coverage.yaml does not match the spec:\n%v", err)
	}
}

// TestClientCallsAreCovered checks that the operations the client calls are
// exactly the operations the manifest marks covered.
func TestClientCallsAreCovered(t *testing.T) {
	s := pinnedSpec(t)
	m := loadManifest(t)
	calls, err := ClientCalls("../discord")
	if err != nil {
		t.Fatal(err)
	}

	called := map[string]bool{}
	for _, c := range calls {
		op, ok := s.Match(c.Method, c.Path)
		if !ok {
			t.Errorf("%s: %s calls %s %s, which is not in the spec", c.Pos, c.Func, c.Method, c.Path)
			continue
		}
		called[op.ID] = true
		fields := s.RequestFields(op)
		for _, q := range c.Query {
			if !slices.Contains(fields, "?"+q) {
				t.Errorf("%s: %s sets query parameter %q, which %s does not accept", c.Pos, c.Func, q, op.ID)
			}
		}
		if e := m.Operations[op.ID]; e.Status != StatusCovered {
			t.Errorf("%s: %s calls %s, which coverage.yaml marks %q; mark it covered and list what uses it", c.Pos, c.Func, op.ID, e.Status)
		}
	}
	for _, id := range m.Covered() {
		if !called[id] {
			t.Errorf("coverage.yaml marks %s covered, but the client does not call it", id)
		}
	}
}

// TestManifestNamesExist checks every "by" entry against the provider's
// registered resources, data sources, functions and actions.
func TestManifestNamesExist(t *testing.T) {
	names := providerNames(t)
	m := loadManifest(t)
	for id, e := range m.Operations {
		for _, by := range e.By {
			if !names[by] {
				t.Errorf("%s: %q is not a resource, data source, function or action of the provider", id, by)
			}
		}
	}
}

func providerNames(t *testing.T) map[string]bool {
	t.Helper()
	ctx := context.Background()
	p := provider.New("test")()
	var meta fwprovider.MetadataResponse
	p.Metadata(ctx, fwprovider.MetadataRequest{}, &meta)
	prefix := meta.TypeName

	names := map[string]bool{}
	for _, f := range p.Resources(ctx) {
		var resp resource.MetadataResponse
		f().Metadata(ctx, resource.MetadataRequest{ProviderTypeName: prefix}, &resp)
		names[resp.TypeName] = true
	}
	for _, f := range p.DataSources(ctx) {
		var resp datasource.MetadataResponse
		f().Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: prefix}, &resp)
		names["data."+resp.TypeName] = true
	}
	if pe, ok := p.(fwprovider.ProviderWithEphemeralResources); ok {
		for _, f := range pe.EphemeralResources(ctx) {
			var resp ephemeral.MetadataResponse
			f().Metadata(ctx, ephemeral.MetadataRequest{ProviderTypeName: prefix}, &resp)
			names["ephemeral."+resp.TypeName] = true
		}
	}
	if pl, ok := p.(fwprovider.ProviderWithListResources); ok {
		for _, f := range pl.ListResources(ctx) {
			var resp resource.MetadataResponse
			f().Metadata(ctx, resource.MetadataRequest{ProviderTypeName: prefix}, &resp)
			names["list."+resp.TypeName] = true
		}
	}
	if pa, ok := p.(fwprovider.ProviderWithActions); ok {
		for _, f := range pa.Actions(ctx) {
			var resp action.MetadataResponse
			f().Metadata(ctx, action.MetadataRequest{ProviderTypeName: prefix}, &resp)
			names["action."+resp.TypeName] = true
		}
	}
	if pf, ok := p.(fwprovider.ProviderWithFunctions); ok {
		for _, f := range pf.Functions(ctx) {
			var resp function.MetadataResponse
			f().Metadata(ctx, function.MetadataRequest{}, &resp)
			names["provider::"+prefix+"::"+resp.Name] = true
		}
	}
	return names
}
