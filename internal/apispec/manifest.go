package apispec

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"

	"gopkg.in/yaml.v3"
)

// Coverage statuses for a spec operation.
const (
	// StatusCovered operations are called by the client on behalf of the
	// resources, data sources, functions or actions listed in By.
	StatusCovered = "covered"
	// StatusPlanned operations are tracked by a roadmap issue.
	StatusPlanned = "planned"
	// StatusOutOfScope operations will not be covered, for Reason.
	StatusOutOfScope = "out_of_scope"
	// StatusPendingDocs operations are in the spec but not yet in Discord's
	// documentation.
	StatusPendingDocs = "pending_docs"
)

// Manifest maps every spec operation to its coverage.
type Manifest struct {
	Operations map[string]Entry `yaml:"operations"`
	// order is the order operations appear in the file.
	order []string
}

// Entry is the coverage of one operation.
type Entry struct {
	Status string   `yaml:"status"`
	By     []string `yaml:"by,omitempty"`
	Issue  int      `yaml:"issue,omitempty"`
	Reason string   `yaml:"reason,omitempty"`
}

// LoadManifest reads a coverage manifest from a file.
func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	return ParseManifest(b)
}

// ParseManifest parses a coverage manifest. Unknown keys are rejected so a
// typo cannot silently drop a field.
func ParseManifest(b []byte) (*Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}

	var doc struct {
		Operations yaml.Node `yaml:"operations"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}
	for i := 0; i+1 < len(doc.Operations.Content); i += 2 {
		m.order = append(m.order, doc.Operations.Content[i].Value)
	}
	return &m, nil
}

// Check validates the manifest against the spec: every spec operation must be
// mapped exactly once, every mapped operation must exist in the spec, and
// every entry must carry the fields its status requires.
func (m *Manifest) Check(s *Spec) error {
	var errs []error
	for _, id := range slices.Sorted(maps.Keys(s.Operations)) {
		if _, ok := m.Operations[id]; !ok {
			op := s.Operations[id]
			errs = append(errs, fmt.Errorf("%s (%s %s) is not in the manifest", id, op.Method, op.Path))
		}
	}
	for _, id := range slices.Sorted(maps.Keys(m.Operations)) {
		if _, ok := s.Operations[id]; !ok {
			errs = append(errs, fmt.Errorf("%s is in the manifest but not in the spec", id))
		}
		if err := m.Operations[id].validate(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
	}
	if !sort.StringsAreSorted(m.order) {
		errs = append(errs, errors.New("operations must be sorted by operationId"))
	}
	return errors.Join(errs...)
}

func (e Entry) validate() error {
	switch e.Status {
	case StatusCovered:
		if len(e.By) == 0 {
			return errors.New("covered operations need by")
		}
		if e.Issue != 0 || e.Reason != "" {
			return errors.New("covered operations take only by")
		}
	case StatusPlanned:
		if e.Issue <= 0 {
			return errors.New("planned operations need issue")
		}
		if len(e.By) != 0 || e.Reason != "" {
			return errors.New("planned operations take only issue")
		}
	case StatusOutOfScope:
		if e.Reason == "" {
			return errors.New("out_of_scope operations need reason")
		}
		if len(e.By) != 0 || e.Issue != 0 {
			return errors.New("out_of_scope operations take only reason")
		}
	case StatusPendingDocs:
		if len(e.By) != 0 || e.Issue != 0 {
			return errors.New("pending_docs operations take at most reason")
		}
	default:
		return fmt.Errorf("unknown status %q", e.Status)
	}
	return nil
}

// Covered returns the IDs of covered operations, sorted.
func (m *Manifest) Covered() []string {
	var ids []string
	for id, e := range m.Operations {
		if e.Status == StatusCovered {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}
