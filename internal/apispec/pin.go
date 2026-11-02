package apispec

// The spec commit the coverage checks run against. To move to a newer spec,
// run `go run ./internal/apispec/cmd/apispec fetch -latest -o .openapi.json`,
// copy the commit and checksum it prints here, and update coverage.yaml until
// `make api-coverage` passes.
const (
	PinnedCommit = "4e75626a4cedfbc397d66f5491c79a9a62a71fd5"
	PinnedSHA256 = "281a2bff563d77a19e51fc3bf8bbe3ebcef28e3dc86684ae395b06ac51ba7484"
)
