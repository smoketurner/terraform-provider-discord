package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

const commandAddress = "discord_application_command.test"

// commandCheckDestroy fails when the command recorded in appID and id still
// exists.
func (e *testEnv) commandCheckDestroy(appID, serverID, id *string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if _, err := e.client.GetApplicationCommand(context.Background(), *appID, *serverID, *id); !discord.IsNotFound(err) {
			return fmt.Errorf("command %s still exists: %w", *id, err)
		}
		return nil
	}
}

func TestAccApplicationCommandGlobal(t *testing.T) {
	env := newTestEnv(t)
	created := `
resource "discord_application_command" "test" {
  name                       = "tf-acc-meetup"
  description                = "Show the next meetup"
  name_localizations         = { de = "tf-acc-treffen" }
  description_localizations  = { de = "Zeigt das nächste Treffen" }
  default_member_permissions = provider::discord::permissions(["MANAGE_GUILD"])
  contexts                   = ["guild", "bot_dm"]
  integration_types          = ["guild_install"]
  nsfw                       = true

  options = [
    {
      type        = "string"
      name        = "when"
      description = "Which meetup"
      required    = true
      choices = [
        { name = "Next", value = "next", name_localizations = { de = "Nächstes" } },
        { name = "Last", value = "last" },
      ]
    },
    {
      type        = "integer"
      name        = "count"
      description = "How many"
      min_value   = 1
      max_value   = 10
    },
    {
      type        = "number"
      name        = "radius"
      description = "Radius in km"
      choices     = [{ name = "Near", value = "2.5" }, { name = "Far", value = "10" }]
    },
    {
      type          = "channel"
      name          = "where"
      description   = "Channel to post in"
      channel_types = ["text", "announcement"]
    },
    {
      type         = "string"
      name         = "city"
      description  = "City"
      min_length   = 2
      max_length   = 50
      autocomplete = true
    },
    {
      type        = "attachment"
      name        = "flyer"
      description = "Flyer"
      file_types  = ["image", ".pdf"]
    },
  ]
}`
	updated := `
resource "discord_application_command" "test" {
  name              = "tf-acc-meetup"
  description       = "Show meetups"
  contexts          = ["guild"]
  integration_types = ["guild_install"]
}`
	ids := statecheck.CompareValue(compare.ValuesSame())
	var appID, serverID, id string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: created,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(commandAddress, "type", "chat_input"),
					resource.TestCheckNoResourceAttr(commandAddress, "server_id"),
					resource.TestCheckResourceAttr(commandAddress, "name_localizations.de", "tf-acc-treffen"),
					resource.TestCheckResourceAttr(commandAddress, "default_member_permissions", "32"),
					resource.TestCheckResourceAttr(commandAddress, "nsfw", "true"),
					resource.TestCheckResourceAttr(commandAddress, "contexts.#", "2"),
					resource.TestCheckResourceAttr(commandAddress, "options.#", "6"),
					resource.TestCheckResourceAttr(commandAddress, "options.0.required", "true"),
					resource.TestCheckResourceAttr(commandAddress, "options.0.autocomplete", "false"),
					resource.TestCheckResourceAttr(commandAddress, "options.0.choices.0.name_localizations.de", "Nächstes"),
					resource.TestCheckResourceAttr(commandAddress, "options.1.min_value", "1"),
					resource.TestCheckResourceAttr(commandAddress, "options.1.required", "false"),
					resource.TestCheckResourceAttr(commandAddress, "options.2.choices.0.value", "2.5"),
					resource.TestCheckResourceAttr(commandAddress, "options.2.choices.1.value", "10"),
					resource.TestCheckTypeSetElemAttr(commandAddress, "options.3.channel_types.*", "announcement"),
					resource.TestCheckResourceAttr(commandAddress, "options.4.max_length", "50"),
					resource.TestCheckResourceAttr(commandAddress, "options.4.autocomplete", "true"),
					resource.TestCheckTypeSetElemAttr(commandAddress, "options.5.file_types.*", ".pdf"),
					captureAttr(commandAddress, "application_id", &appID),
					captureAttr(commandAddress, "id", &id),
				),
				ConfigStateChecks: []statecheck.StateCheck{ids.AddStateValue(commandAddress, tfjsonpath.New("id"))},
			},
			{
				ResourceName:      commandAddress,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return appID + "/" + id, nil },
			},
			{
				// Removing arguments clears them on Discord.
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(commandAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(commandAddress, "description", "Show meetups"),
					resource.TestCheckNoResourceAttr(commandAddress, "options"),
					resource.TestCheckNoResourceAttr(commandAddress, "name_localizations"),
					resource.TestCheckNoResourceAttr(commandAddress, "description_localizations"),
					resource.TestCheckNoResourceAttr(commandAddress, "default_member_permissions"),
					resource.TestCheckResourceAttr(commandAddress, "nsfw", "false"),
					resource.TestCheckResourceAttr(commandAddress, "contexts.#", "1"),
					func(*terraform.State) error {
						c, err := env.client.GetApplicationCommand(context.Background(), appID, "", id)
						if err != nil {
							return err
						}
						if len(c.Options) != 0 || c.NameLocalizations != nil || c.DefaultMemberPermissions != nil {
							return fmt.Errorf("arguments were not cleared on Discord: %+v", c)
						}
						return nil
					},
				),
				ConfigStateChecks: []statecheck.StateCheck{ids.AddStateValue(commandAddress, tfjsonpath.New("id"))},
			},
			{
				// The command is edited outside Terraform: changed back.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					_, err := c.EditApplicationCommand(ctx, appID, "", id, discord.Payload{
						"description": "elsewhere", "contexts": []int{0, 1, 2},
					})
					return err
				}),
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(commandAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(commandAddress, "description", "Show meetups"),
					resource.TestCheckResourceAttr(commandAddress, "contexts.#", "1"),
				),
			},
			{
				// The command is deleted outside Terraform: created again.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					return c.DeleteApplicationCommand(ctx, appID, "", id)
				}),
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(commandAddress, plancheck.ResourceActionCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					attrDiffers(commandAddress, "id", &id),
					captureAttr(commandAddress, "id", &id),
				),
			},
		},
		CheckDestroy: env.commandCheckDestroy(&appID, &serverID, &id),
	})
}

func TestAccApplicationCommandServer(t *testing.T) {
	env := newTestEnv(t)
	cfg := func(attrs string) string {
		return env.config(`
resource "discord_application_command" "test" {
  server_id = local.server_id
` + attrs + `
}`)
	}
	nested := cfg(`  name        = "tf-acc-config"
  description = "Configure the bot"
  options = [
    {
      type        = "sub_command_group"
      name        = "roles"
      description = "Manage roles"
      options = [
        {
          type        = "sub_command"
          name        = "add"
          description = "Add a role"
          options = [
            { type = "role", name = "role", description = "Role to add", required = true },
            { type = "boolean", name = "notify", description = "Notify members" },
          ]
        },
      ]
    },
    {
      type        = "sub_command"
      name        = "reset"
      description = "Reset everything"
    },
  ]`)
	user := cfg(`  type = "user"
  name = "TF Acc High Five"`)
	var appID, serverID, id string
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: nested,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(commandAddress, "server_id", env.serverID),
					resource.TestCheckNoResourceAttr(commandAddress, "contexts"),
					resource.TestCheckNoResourceAttr(commandAddress, "integration_types"),
					resource.TestCheckResourceAttr(commandAddress, "options.0.options.0.options.0.type", "role"),
					resource.TestCheckResourceAttr(commandAddress, "options.0.options.0.options.0.required", "true"),
					resource.TestCheckResourceAttr(commandAddress, "options.0.options.0.options.1.name", "notify"),
					resource.TestCheckNoResourceAttr(commandAddress, "options.1.options"),
					captureAttr(commandAddress, "application_id", &appID),
					captureAttr(commandAddress, "server_id", &serverID),
					captureAttr(commandAddress, "id", &id),
				),
			},
			{
				ResourceName:      commandAddress,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return appID + "/" + serverID + "/" + id, nil },
			},
			{
				// A nested option is edited outside Terraform: changed back.
				PreConfig: env.outsideTerraform(func(ctx context.Context, c *discord.Client) error {
					cmd, err := c.GetApplicationCommand(ctx, appID, serverID, id)
					if err != nil {
						return err
					}
					cmd.Options[0].Options[0].Options[0].Description = "elsewhere"
					_, err = c.EditApplicationCommand(ctx, appID, serverID, id, discord.Payload{"options": cmd.Options})
					return err
				}),
				Config: nested,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(commandAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr(commandAddress, "options.0.options.0.options.0.description", "Role to add"),
			},
			{
				// Changing the type replaces the command.
				Config: user,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(commandAddress, plancheck.ResourceActionReplace)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(commandAddress, "type", "user"),
					resource.TestCheckResourceAttr(commandAddress, "name", "TF Acc High Five"),
					resource.TestCheckNoResourceAttr(commandAddress, "description"),
					attrDiffers(commandAddress, "id", &id),
					captureAttr(commandAddress, "id", &id),
				),
			},
		},
		CheckDestroy: env.commandCheckDestroy(&appID, &serverID, &id),
	})
}

func TestAccApplicationCommandIdentity(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		TerraformVersionChecks: requiresIdentity,
		Steps: []resource.TestStep{
			{
				Config: `
resource "discord_application_command" "test" {
  type = "message"
  name = "TF Acc Bookmark"
}`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectIdentity(commandAddress, map[string]knownvalue.Check{
						"application_id": knownvalue.NotNull(),
						"server_id":      knownvalue.Null(),
						"command_id":     knownvalue.NotNull(),
					}),
					statecheck.ExpectIdentityValueMatchesStateAtPath(commandAddress, tfjsonpath.New("command_id"), tfjsonpath.New("id")),
				},
			},
			identityImportStep(commandAddress),
		},
	})
}

func TestAccApplicationCommandImportErrors(t *testing.T) {
	env := newTestEnv(t)
	cfg := `
resource "discord_application_command" "test" {
  type = "message"
  name = "TF Acc Bookmark"
}`
	importStep := func(id string) resource.TestStep {
		return resource.TestStep{
			Config:        cfg,
			ResourceName:  commandAddress,
			ImportState:   true,
			ImportStateId: id,
			ExpectError:   regexp.MustCompile(`expected import ID in the form`),
		}
	}
	env.run(resource.TestCase{
		Steps: []resource.TestStep{
			importStep("123"),
			importStep("1/2/3/4"),
			importStep("1//3"),
		},
	})
}

func TestAccApplicationCommandIdentityImportErrors(t *testing.T) {
	env := newTestEnv(t)
	env.run(resource.TestCase{
		TerraformVersionChecks: requiresIdentity,
		Steps: []resource.TestStep{{
			Config: `
import {
  to       = discord_application_command.test
  identity = { application_id = "1", command_id = "" }
}
resource "discord_application_command" "test" {
  type = "message"
  name = "TF Acc Bookmark"
}`,
			ExpectError: regexp.MustCompile(`Identity attribute "command_id" must not be empty`),
		}},
	})
}

func TestAccApplicationCommandWrongApplication(t *testing.T) {
	env := newTestEnv(t)
	env.requireFake()
	env.run(resource.TestCase{
		Steps: []resource.TestStep{{
			Config: `
resource "discord_application_command" "test" {
  application_id = "123456789012345678"
  type           = "user"
  name           = "TF Acc High Five"
}`,
			ExpectError: regexp.MustCompile(`Unable to create application command`),
		}},
	})
}

func TestAccApplicationCommandValidation(t *testing.T) {
	env := newTestEnv(t)
	command := func(attrs string) string {
		return env.config(`
resource "discord_application_command" "test" {
` + attrs + `
}`)
	}
	option := func(attrs string) string {
		return command(`  name        = "tf-acc"
  description = "Test"
  options = [
` + attrs + `
  ]`)
	}
	cases := map[string]struct {
		config string
		err    string
	}{
		"uppercase chat_input name": {command(`  name = "Meetup"
  description = "Test"`), `Invalid command name`},
		"space in chat_input name": {command(`  name = "meet up"
  description = "Test"`), `Invalid command name`},
		"uppercase localized name": {command(`  name = "meetup"
  description = "Test"
  name_localizations = { de = "Treffen" }`), `Invalid command name`},
		"unknown locale": {command(`  name = "meetup"
  description = "Test"
  name_localizations = { xx = "treffen" }`), `value must be one of`},
		"missing description": {command(`  name = "meetup"`), `chat_input commands require a description`},
		"user command description": {command(`  type = "user"
  name = "High Five"
  description = "Test"`), `description only applies to chat_input commands`},
		"message command options": {command(`  type = "message"
  name = "Bookmark"
  options = [{ type = "string", name = "x", description = "x" }]`), `options only applies to chat_input commands`},
		"server command contexts": {command(`  server_id = local.server_id
  name = "meetup"
  description = "Test"
  contexts = ["guild"]`), `contexts only applies to global commands`},
		"server command integration types": {command(`  server_id = local.server_id
  name = "meetup"
  description = "Test"
  integration_types = ["guild_install"]`), `integration_types only applies to global commands`},
		"duplicate option name": {option(`    { type = "string", name = "x", description = "x" },
    { type = "integer", name = "x", description = "x" },`), `Duplicate option name`},
		"required after optional": {option(`    { type = "string", name = "x", description = "x" },
    { type = "string", name = "y", description = "y", required = true },`), `Required options must be listed before optional options`},
		"group in subcommand": {option(`    { type = "sub_command", name = "x", description = "x", options = [
      { type = "sub_command_group", name = "y", description = "y" },
    ] },`), `A sub_command may only contain parameters`},
		"parameter in group": {option(`    { type = "sub_command_group", name = "x", description = "x", options = [
      { type = "string", name = "y", description = "y" },
    ] },`), `A sub_command_group may only contain sub_command options`},
		"nested group": {option(`    { type = "sub_command_group", name = "x", description = "x", options = [
      { type = "sub_command_group", name = "y", description = "y" },
    ] },`), `A sub_command_group may only contain sub_command options`},
		"options on a parameter": {option(`    { type = "string", name = "x", description = "x", options = [
      { type = "string", name = "y", description = "y" },
    ] },`), `options does not apply to string options`},
		"required subcommand":              {option(`    { type = "sub_command", name = "x", description = "x", required = true },`), `required does not apply to sub_command options`},
		"choices on a boolean":             {option(`    { type = "boolean", name = "x", description = "x", choices = [{ name = "a", value = "a" }] },`), `choices does not apply to boolean options`},
		"autocomplete with choices":        {option(`    { type = "string", name = "x", description = "x", autocomplete = true, choices = [{ name = "a", value = "a" }] },`), `autocomplete cannot be enabled on an option with choices`},
		"channel types on a string":        {option(`    { type = "string", name = "x", description = "x", channel_types = ["text"] },`), `channel_types does not apply to string options`},
		"min value on a string":            {option(`    { type = "string", name = "x", description = "x", min_value = 1 },`), `min_value does not apply to string options`},
		"min length on an integer":         {option(`    { type = "integer", name = "x", description = "x", min_length = 1 },`), `min_length does not apply to integer options`},
		"file types on a user":             {option(`    { type = "user", name = "x", description = "x", file_types = ["image"] },`), `file_types does not apply to user options`},
		"fractional integer bound":         {option(`    { type = "integer", name = "x", description = "x", min_value = 1.5 },`), `Invalid integer bound`},
		"min value above max":              {option(`    { type = "number", name = "x", description = "x", min_value = 2, max_value = 1 },`), `min_value must not exceed max_value`},
		"min length above max":             {option(`    { type = "string", name = "x", description = "x", min_length = 5, max_length = 1 },`), `min_length must not exceed max_length`},
		"non-integer choice":               {option(`    { type = "integer", name = "x", description = "x", choices = [{ name = "a", value = "1.5" }] },`), `must be a whole number`},
		"integer choice with leading zero": {option(`    { type = "integer", name = "x", description = "x", choices = [{ name = "a", value = "01" }] },`), `must be a whole number`},
		"non-canonical number choice":      {option(`    { type = "number", name = "x", description = "x", choices = [{ name = "a", value = "2.50" }] },`), `shortest decimal form`},
		"long string choice":               {option(`    { type = "string", name = "x", description = "x", choices = [{ name = "a", value = "` + fmt.Sprintf("%0101d", 0) + `" }] },`), `longer than 100 characters`},
		"uppercase option name":            {option(`    { type = "string", name = "X", description = "x" },`), `Invalid command name`},
		"unknown channel type":             {option(`    { type = "channel", name = "x", description = "x", channel_types = ["dungeon"] },`), `value must be one of`},
		"empty options":                    {option(``), `list must contain at least 1 elements`},
	}
	steps := make([]resource.TestStep, 0, len(cases))
	for name, c := range cases {
		steps = append(steps, resource.TestStep{
			Config:      c.config,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(regexp.QuoteMeta(c.err)),
			PreConfig:   func() { t.Log(name) },
		})
	}
	env.run(resource.TestCase{Steps: steps})
}
