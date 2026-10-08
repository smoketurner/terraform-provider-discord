# Contributing

## Setup

1. Install Go (the version in `go.mod`), Terraform 1.8+, [golangci-lint](https://golangci-lint.run) and
   [prek](https://github.com/j178/prek).
2. Run `prek install` to enable the Git hooks.

## Making changes

- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org) (`feat:`, `fix:`, `docs:`,
  `chore:`). release-please uses them to choose the next version and write the changelog.
- Check new API fields, endpoints and limits against the
  [Discord API documentation](https://github.com/discord/discord-api-docs). Do not rely on memory or on other client
  libraries.
- Add or update acceptance tests in `internal/provider`. Cover drift, meaning changes made outside Terraform, and
  import. If the change relies on Discord behavior the fake does not model yet, extend the fake in
  `internal/discord/discordtest`.
- After changing a schema or an example, run `make generate` and commit the updated `docs/`.

## Tests

`make test` runs every test, including the Terraform acceptance tests, against an in-memory fake of the Discord API.
It needs no credentials.

To run the same tests against a real server, use a throwaway test server, because the tests create and delete roles,
channels and messages:

```shell
export TF_ACC=1
export DISCORD_TOKEN=...          # bot token
export DISCORD_SERVER_ID=...      # test server; Community enabled for announcement/stage tests
export DISCORD_TEST_USER_ID=...   # optional: a member for discord_member_role tests
make testacc
```

The bot needs the Administrator permission on the test server. The **Live acceptance tests** workflow runs these
tests weekly once the `discord-live` environment is configured.

## Debugging

Build and install the provider locally, then point Terraform at it with a
[development override](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers):

```hcl
# ~/.terraformrc
provider_installation {
  dev_overrides {
    "smoketurner/discord" = "/path/to/go/bin"
  }
  direct {}
}
```

Run Terraform with `TF_LOG=DEBUG` to see the provider's logs. To use a debugger, start the provider with
`go run . -debug` and export the `TF_REATTACH_PROVIDERS` value it prints.

## Releases

Releases are automated:

1. Pushes to `main` update a release pull request that bumps the version and the changelog.
2. Merging that pull request tags the commit and creates a draft GitHub release.
3. GoReleaser builds every platform, signs the checksums with GPG, uploads them to the draft and publishes it.
4. The Terraform Registry picks up the published release through its webhook.

### One-time setup

- Create the `release` GitHub environment with secrets `GPG_PRIVATE_KEY` (ASCII-armored RSA private key) and
  `PASSPHRASE`.
- Add the matching public key to the Terraform Registry under **User Settings → Signing Keys**.
- Optionally add a `RELEASE_PLEASE_TOKEN` secret, a fine-grained token with contents and pull request write access,
  so that CI runs on release pull requests.
- After the first release exists, publish the provider at
  [registry.terraform.io](https://registry.terraform.io/publish/provider).
