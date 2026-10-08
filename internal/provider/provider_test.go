package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
	"github.com/smoketurner/terraform-provider-discord/internal/discord/discordtest"
)

var protoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"discord": providerserver.NewProtocol6WithError(New("test")()),
}

// testEnv runs acceptance tests against the in-memory fake Discord API, or
// against a real server when TF_ACC, DISCORD_TOKEN and DISCORD_SERVER_ID are
// set. Live runs create and delete real roles, channels and messages.
type testEnv struct {
	t        *testing.T
	live     bool
	fake     *discordtest.Server
	client   *discord.Client
	serverID string
	userID   string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	if os.Getenv("TF_ACC") != "" && os.Getenv("DISCORD_TOKEN") != "" && os.Getenv("DISCORD_SERVER_ID") != "" {
		return &testEnv{
			t:        t,
			live:     true,
			client:   discord.NewClient(os.Getenv("DISCORD_BASE_URL"), os.Getenv("DISCORD_TOKEN"), "test"),
			serverID: os.Getenv("DISCORD_SERVER_ID"),
			userID:   os.Getenv("DISCORD_TEST_USER_ID"),
		}
	}
	fake := discordtest.NewServer()
	t.Cleanup(fake.Close)
	t.Setenv("DISCORD_TOKEN", discordtest.Token)
	t.Setenv("DISCORD_BASE_URL", fake.URL)
	return &testEnv{
		t:        t,
		fake:     fake,
		client:   discord.NewClient(fake.URL, discordtest.Token, "test"),
		serverID: discordtest.GuildID,
		userID:   discordtest.UserID,
	}
}

func (e *testEnv) run(tc resource.TestCase) {
	e.t.Helper()
	if tc.ProtoV6ProviderFactories == nil {
		tc.ProtoV6ProviderFactories = protoV6ProviderFactories
	}
	if e.live {
		resource.Test(e.t, tc)
		return
	}
	resource.UnitTest(e.t, tc)
}

func (e *testEnv) requireUser() {
	e.t.Helper()
	if e.userID == "" {
		e.t.Skip("DISCORD_TEST_USER_ID is required for member tests against a live server")
	}
}

func (e *testEnv) requireFake() {
	e.t.Helper()
	if e.live {
		e.t.Skip("test relies on the fake Discord API")
	}
}

// config prefixes HCL with a server_id local.
func (e *testEnv) config(hcl string) string {
	return fmt.Sprintf("locals {\n  server_id = %q\n  user_id = %q\n}\n", e.serverID, e.userID) + hcl
}

// captureAttr stores a resource attribute from state for use in later steps.
func captureAttr(name, attr string, dst *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("resource %s not found in state", name)
		}
		*dst = rs.Primary.Attributes[attr]
		if *dst == "" {
			return fmt.Errorf("%s.%s is empty", name, attr)
		}
		return nil
	}
}

// outsideTerraform runs a client call in a PreConfig hook, failing the test
// on error, to simulate changes made in the Discord client.
func (e *testEnv) outsideTerraform(f func(ctx context.Context, c *discord.Client) error) func() {
	return func() {
		if err := f(context.Background(), e.client); err != nil {
			e.t.Fatalf("changing state outside Terraform: %v", err)
		}
	}
}

func TestProviderMissingToken(t *testing.T) {
	t.Setenv("DISCORD_TOKEN", "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      `data "discord_server" "s" { id = "1" }`,
			ExpectError: regexp.MustCompile(`Missing Discord bot token`),
		}},
	})
}

func TestProviderTokenWithBotPrefix(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "discord" {
  token = "Bot %s"
}
data "discord_server" "s" { id = %q }`, discordtest.Token, env.serverID),
			Check: resource.TestCheckResourceAttr("data.discord_server.s", "name", "Test Server"),
		}},
	})
}

func TestProviderInvalidToken(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "discord" {
  token = "wrong"
}
data "discord_server" "s" { id = %q }`, env.serverID),
			ExpectError: regexp.MustCompile(`HTTP 401`),
		}},
	})
}
