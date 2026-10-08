package apispec

// The spec commit the coverage checks run against. To move to a newer spec,
// run `go run ./internal/apispec/cmd/apispec fetch -latest -o .openapi.json`,
// copy the commit and checksum it prints here, and update coverage.yaml until
// `make api-coverage` passes.
const (
	PinnedCommit = "c0e6a86aca23a40862cc9e58b7ecf60865158854"
	PinnedSHA256 = "12308009248ae5be59aa27b3539468c9e3fdd8fb433df8430eabf9bbe5a278f7"
)
