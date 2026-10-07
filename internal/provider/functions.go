package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

var (
	_ function.Function = permissionsFunction{}
	_ function.Function = colorFunction{}
)

type permissionsFunction struct{}

func newPermissionsFunction() function.Function { return permissionsFunction{} }

func (permissionsFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "permissions"
}

func (permissionsFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Build a permission bitfield from permission names",
		MarkdownDescription: "Combines Discord permission flag names (case-insensitive) into the decimal string used by " +
			"`permissions`, `allow` and `deny` attributes. Valid names: " + strings.Join(backtick(discord.PermissionNames()), ", ") + ".",
		Parameters: []function.Parameter{
			function.SetParameter{
				Name:                "names",
				ElementType:         types.StringType,
				MarkdownDescription: "Permission names, e.g. `[\"VIEW_CHANNEL\", \"SEND_MESSAGES\"]`.",
			},
		},
		Return: function.StringReturn{},
	}
}

func (permissionsFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var names []string
	resp.Error = function.ConcatFuncErrors(req.Arguments.Get(ctx, &names))
	if resp.Error != nil {
		return
	}
	bits, err := discord.PermissionBits(names)
	if err != nil {
		resp.Error = function.NewArgumentFuncError(0, err.Error())
		return
	}
	resp.Error = function.ConcatFuncErrors(resp.Result.Set(ctx, bits))
}

func backtick(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = "`" + v + "`"
	}
	return out
}

type colorFunction struct{}

func newColorFunction() function.Function { return colorFunction{} }

func (colorFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "color"
}

func (colorFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:             "Convert a hex color to an integer",
		MarkdownDescription: "Converts a hex RGB color such as `#5865F2`, `5865f2` or `#fff` to the integer Discord uses for role and embed colors.",
		Parameters: []function.Parameter{
			function.StringParameter{Name: "hex", MarkdownDescription: "Hex color, with or without a leading `#`."},
		},
		Return: function.Int64Return{},
	}
}

// parseHexColor parses #rgb or #rrggbb, with the "#" optional.
func parseHexColor(s string) (int64, error) {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return 0, fmt.Errorf("invalid hex color %q: expected #rgb or #rrggbb", s)
	}
	v, err := strconv.ParseInt(h, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid hex color %q: expected #rgb or #rrggbb", s)
	}
	return v, nil
}

func (colorFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var hex string
	resp.Error = function.ConcatFuncErrors(req.Arguments.Get(ctx, &hex))
	if resp.Error != nil {
		return
	}
	v, err := parseHexColor(hex)
	if err != nil {
		resp.Error = function.NewArgumentFuncError(0, err.Error())
		return
	}
	resp.Error = function.ConcatFuncErrors(resp.Result.Set(ctx, v))
}
