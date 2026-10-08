package apispec

import (
	"fmt"
	"slices"
	"strings"
)

// Finding is a spec change that needs an issue. Title identifies the change,
// so the watcher workflow updates an existing issue instead of opening a
// duplicate.
type Finding struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Watch compares the latest spec with the pinned spec and the manifest. It
// reports operations the manifest does not map, and request fields or query
// parameters added to covered operations since the pinned spec.
func Watch(pinned, latest *Spec, m *Manifest, latestCommit string) []Finding {
	source := fmt.Sprintf("https://github.com/discord/discord-api-spec/blob/%s/specs/openapi.json", latestCommit)
	var out []Finding
	for id, op := range latest.Operations {
		if _, ok := m.Operations[id]; ok {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Discord's OpenAPI spec has a new operation, `%s`:\n\n", id)
		fmt.Fprintf(&b, "- `%s %s`\n", op.Method, op.Path)
		if op.Summary != "" {
			fmt.Fprintf(&b, "- Summary: %s\n", op.Summary)
		}
		if auth := securitySchemes(op); auth != "" {
			fmt.Fprintf(&b, "- Authentication: %s\n", auth)
		}
		fmt.Fprintf(&b, "\nSource: %s\n\n", source)
		b.WriteString("Map it in `coverage.yaml` as `covered`, `planned`, `out_of_scope` or `pending_docs`.\n")
		out = append(out, Finding{Title: "API spec: new operation " + id, Body: b.String()})
	}

	for _, id := range m.Covered() {
		before, ok1 := pinned.Operations[id]
		after, ok2 := latest.Operations[id]
		if !ok1 || !ok2 {
			continue
		}
		known := pinned.RequestFields(before)
		for _, field := range latest.RequestFields(after) {
			if slices.Contains(known, field) {
				continue
			}
			kind, name := "request field", field
			if q, ok := strings.CutPrefix(field, "?"); ok {
				kind, name = "query parameter", q
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Discord's OpenAPI spec added the %s `%s` to `%s` (`%s %s`).\n\n", kind, name, id, after.Method, after.Path)
			fmt.Fprintf(&b, "Used by: %s\n\n", strings.Join(backticked(m.Operations[id].By), ", "))
			fmt.Fprintf(&b, "Source: %s\n\n", source)
			b.WriteString("Check the Discord documentation, then support the field or record why not. The watcher reports it until the pinned spec in `internal/apispec/pin.go` includes it.\n")
			out = append(out, Finding{Title: fmt.Sprintf("API spec: new %s %s on %s", kind, name, id), Body: b.String()})
		}
	}
	slices.SortFunc(out, func(a, b Finding) int { return strings.Compare(a.Title, b.Title) })
	return out
}

func securitySchemes(op *Operation) string {
	var names []string
	for _, req := range op.Security {
		if len(req) == 0 {
			names = append(names, "none")
		}
		for name := range req {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return strings.Join(slices.Compact(names), ", ")
}

func backticked(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = "`" + v + "`"
	}
	return out
}
