package provider

import (
	"context"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// maxMonetizationPage is the most entitlements or subscriptions Discord
// returns per request.
const maxMonetizationPage = 100

var (
	skuTypes          = enumMapping{"", "", "durable", "consumable", "", "subscription", "subscription_group"}
	entitlementTypes  = enumMapping{"", "purchase", "premium_subscription", "developer_gift", "test_mode_purchase", "free_purchase", "user_gift", "premium_purchase", "application_subscription"}
	subscriptionTypes = enumMapping{"active", "inactive", "ending"}
)

func applicationIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "ID of the application. Defaults to the application of the bot token.",
		Optional:            true,
		Computed:            true,
		Validators:          []validator.String{snowflakeValidator()},
	}
}

func optionalSnowflake(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Optional: true, Validators: []validator.String{snowflakeValidator()}}
}

func optionalLimit(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{MarkdownDescription: desc, Optional: true, Validators: []validator.Int64{int64validator.AtLeast(1)}}
}

// resolveApplicationID returns the configured application ID, or the bot's
// own application when none is configured.
func resolveApplicationID(ctx context.Context, c *discord.Client, id types.String, diags *diag.Diagnostics) (types.String, bool) {
	if !id.IsNull() {
		return id, true
	}
	app, err := c.GetCurrentApplication(ctx)
	if err != nil {
		apiError(diags, "read the bot's application", err)
		return id, false
	}
	return types.StringValue(app.ID), true
}

// fetchPages collects up to limit items of a list paginated by ID, starting
// after p.After when it is set and otherwise before p.Before (or from the
// newest). Discord does not document the order within a page, so the next
// cursor is the highest or lowest ID seen. The result is sorted by ID.
func fetchPages[T any](p discord.Page, limit int64, fetch func(discord.Page) ([]T, error), id func(T) string) ([]T, error) {
	var all []T
	for remaining := limit; remaining > 0; {
		p.Limit = int(min(remaining, maxMonetizationPage))
		items, err := fetch(p)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) < p.Limit {
			break
		}
		remaining -= int64(len(items))
		ids := make([]string, len(items))
		for i, it := range items {
			ids[i] = id(it)
		}
		if p.After != "" {
			p.After = slices.MaxFunc(ids, compareSnowflakes)
		} else {
			p.Before = slices.MinFunc(ids, compareSnowflakes)
		}
	}
	slices.SortFunc(all, func(a, b T) int { return compareSnowflakes(id(a), id(b)) })
	return all, nil
}

func compareSnowflakes(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// SKUs.

type skusDataSource struct{ readOnlyDataSource }

type skuModel struct {
	ID    types.String `tfsdk:"id"`
	Type  types.String `tfsdk:"type"`
	Name  types.String `tfsdk:"name"`
	Slug  types.String `tfsdk:"slug"`
	Flags types.Int64  `tfsdk:"flags"`
}

type skusDataModel struct {
	ApplicationID types.String `tfsdk:"application_id"`
	SKUs          []skuModel   `tfsdk:"skus"`
}

func newSKUsDataSource() datasource.DataSource { return &skusDataSource{} }

func (d *skusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skus"
}

func (d *skusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the SKUs (premium offerings) of an application. Each subscription has two SKUs: " +
			"use the one of type `subscription` for entitlements, not the `subscription_group` Discord creates with it.",
		Attributes: map[string]schema.Attribute{
			"application_id": applicationIDAttribute(),
			"skus": computedList("SKUs.", map[string]schema.Attribute{
				"id":    computedString("SKU ID."),
				"type":  computedString("SKU type: " + skuTypes.doc() + "."),
				"name":  computedString("Customer-facing name."),
				"slug":  computedString("URL slug generated from the name."),
				"flags": computedInt("[SKU flags](https://docs.discord.com/developers/resources/sku#sku-object-sku-flags) bitfield, such as whether a subscription is for servers or users."),
			}),
		},
	}
}

func (d *skusDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m skusDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var ok bool
	if m.ApplicationID, ok = resolveApplicationID(ctx, d.client, m.ApplicationID, &resp.Diagnostics); !ok {
		return
	}
	skus, err := d.client.ListSKUs(ctx, m.ApplicationID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "list SKUs", err)
		return
	}
	m.SKUs = []skuModel{}
	for _, s := range skus {
		m.SKUs = append(m.SKUs, skuModel{
			ID:    types.StringValue(s.ID),
			Type:  skuTypes.name(s.Type),
			Name:  types.StringValue(s.Name),
			Slug:  types.StringValue(s.Slug),
			Flags: types.Int64Value(s.Flags),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Entitlements.

type entitlementsDataSource struct{ readOnlyDataSource }

type entitlementModel struct {
	ID       types.String `tfsdk:"id"`
	SKUID    types.String `tfsdk:"sku_id"`
	UserID   types.String `tfsdk:"user_id"`
	ServerID types.String `tfsdk:"server_id"`
	Type     types.String `tfsdk:"type"`
	Deleted  types.Bool   `tfsdk:"deleted"`
	StartsAt types.String `tfsdk:"starts_at"`
	EndsAt   types.String `tfsdk:"ends_at"`
	Consumed types.Bool   `tfsdk:"consumed"`
}

type entitlementsDataModel struct {
	ApplicationID  types.String       `tfsdk:"application_id"`
	UserID         types.String       `tfsdk:"user_id"`
	ServerID       types.String       `tfsdk:"server_id"`
	SKUIDs         types.Set          `tfsdk:"sku_ids"`
	ExcludeEnded   types.Bool         `tfsdk:"exclude_ended"`
	ExcludeDeleted types.Bool         `tfsdk:"exclude_deleted"`
	Before         types.String       `tfsdk:"before"`
	After          types.String       `tfsdk:"after"`
	Limit          types.Int64        `tfsdk:"limit"`
	Entitlements   []entitlementModel `tfsdk:"entitlements"`
}

func newEntitlementsDataSource() datasource.DataSource { return &entitlementsDataSource{} }

func (d *entitlementsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_entitlements"
}

func (d *entitlementsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the entitlements of an application, active and expired, sorted by ID. An " +
			"entitlement grants a user or server access to an SKU.",
		Attributes: map[string]schema.Attribute{
			"application_id": applicationIDAttribute(),
			"user_id":        optionalSnowflake("Only return entitlements of this user."),
			"server_id":      optionalSnowflake("Only return entitlements of this server."),
			"sku_ids": schema.SetAttribute{
				MarkdownDescription: "Only return entitlements to these SKUs.",
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          []validator.Set{setvalidator.ValueStringsAre(snowflakeValidator())},
			},
			"exclude_ended":   schema.BoolAttribute{MarkdownDescription: "Omit entitlements that have ended. Defaults to `false`.", Optional: true},
			"exclude_deleted": schema.BoolAttribute{MarkdownDescription: "Omit deleted entitlements. Defaults to `true`.", Optional: true},
			"before":          optionalSnowflake("Only return entitlements with an ID lower than this one. Without `after`, the newest entitlements are returned."),
			"after":           optionalSnowflake("Only return entitlements with an ID higher than this one, starting with the oldest."),
			"limit":           optionalLimit("Maximum number of entitlements to return. Defaults to 100. Discord returns at most 100 per request, so higher limits take several requests."),
			"entitlements": computedList("Entitlements.", map[string]schema.Attribute{
				"id":        computedString("Entitlement ID."),
				"sku_id":    computedString("ID of the SKU."),
				"user_id":   computedString("ID of the user granted the SKU."),
				"server_id": computedString("ID of the server granted the SKU."),
				"type":      computedString("Entitlement type: " + entitlementTypes.doc() + "."),
				"deleted":   computedBool("Whether the entitlement was deleted."),
				"starts_at": computedString("When the entitlement starts, or null."),
				"ends_at":   computedString("When the entitlement ends, or null."),
				"consumed":  computedBool("For consumable SKUs, whether the entitlement has been consumed."),
			}),
		},
	}
}

func (d *entitlementsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m entitlementsDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var ok bool
	if m.ApplicationID, ok = resolveApplicationID(ctx, d.client, m.ApplicationID, &resp.Diagnostics); !ok {
		return
	}
	q := discord.EntitlementQuery{UserID: m.UserID.ValueString(), GuildID: m.ServerID.ValueString(), ExcludeEnded: m.ExcludeEnded.ValueBool()}
	if !m.ExcludeDeleted.IsNull() {
		q.ExcludeDeleted = m.ExcludeDeleted.ValueBoolPointer()
	}
	if !m.SKUIDs.IsNull() {
		resp.Diagnostics.Append(m.SKUIDs.ElementsAs(ctx, &q.SKUIDs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		slices.Sort(q.SKUIDs)
	}
	limit := int64(100)
	if !m.Limit.IsNull() {
		limit = m.Limit.ValueInt64()
	}
	appID := m.ApplicationID.ValueString()
	all, err := fetchPages(discord.Page{Before: m.Before.ValueString(), After: m.After.ValueString()}, limit,
		func(p discord.Page) ([]discord.Entitlement, error) {
			q.Page = p
			return d.client.ListEntitlements(ctx, appID, q)
		},
		func(e discord.Entitlement) string { return e.ID })
	if err != nil {
		apiError(&resp.Diagnostics, "list entitlements", err)
		return
	}
	m.Entitlements = []entitlementModel{}
	for _, e := range all {
		em := entitlementModel{
			ID:       types.StringValue(e.ID),
			SKUID:    types.StringValue(e.SKUID),
			UserID:   stringPtrValue(e.UserID),
			ServerID: stringPtrValue(e.GuildID),
			Type:     entitlementTypes.name(e.Type),
			Deleted:  types.BoolValue(e.Deleted),
			StartsAt: stringPtrValue(e.StartsAt),
			EndsAt:   stringPtrValue(e.EndsAt),
			Consumed: types.BoolPointerValue(e.Consumed),
		}
		m.Entitlements = append(m.Entitlements, em)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// SKU subscriptions.

type skuSubscriptionsDataSource struct{ readOnlyDataSource }

type subscriptionModel struct {
	ID                 types.String `tfsdk:"id"`
	UserID             types.String `tfsdk:"user_id"`
	SKUIDs             types.List   `tfsdk:"sku_ids"`
	EntitlementIDs     types.List   `tfsdk:"entitlement_ids"`
	RenewalSKUIDs      types.List   `tfsdk:"renewal_sku_ids"`
	CurrentPeriodStart types.String `tfsdk:"current_period_start"`
	CurrentPeriodEnd   types.String `tfsdk:"current_period_end"`
	Status             types.String `tfsdk:"status"`
	CanceledAt         types.String `tfsdk:"canceled_at"`
}

type skuSubscriptionsDataModel struct {
	SKUID         types.String        `tfsdk:"sku_id"`
	UserID        types.String        `tfsdk:"user_id"`
	Before        types.String        `tfsdk:"before"`
	After         types.String        `tfsdk:"after"`
	Limit         types.Int64         `tfsdk:"limit"`
	Subscriptions []subscriptionModel `tfsdk:"subscriptions"`
}

func newSKUSubscriptionsDataSource() datasource.DataSource { return &skuSubscriptionsDataSource{} }

func (d *skuSubscriptionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sku_subscriptions"
}

func (d *skuSubscriptionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists a user's subscriptions that include an SKU, sorted by ID. Use entitlements, not the " +
			"subscription status, to decide whether the user has access.",
		Attributes: map[string]schema.Attribute{
			"sku_id":  requiredSnowflake("ID of the SKU."),
			"user_id": requiredSnowflake("ID of the user. Discord requires it for bots."),
			"before":  optionalSnowflake("Only return subscriptions with an ID lower than this one."),
			"after":   optionalSnowflake("Only return subscriptions with an ID higher than this one, starting with the oldest."),
			"limit":   optionalLimit("Maximum number of subscriptions to return. Defaults to 50. Discord returns at most 100 per request, so higher limits take several requests."),
			"subscriptions": computedList("Subscriptions.", map[string]schema.Attribute{
				"id":                   computedString("Subscription ID."),
				"user_id":              computedString("ID of the subscribed user."),
				"sku_ids":              schema.ListAttribute{MarkdownDescription: "IDs of the SKUs subscribed to.", ElementType: types.StringType, Computed: true},
				"entitlement_ids":      schema.ListAttribute{MarkdownDescription: "IDs of the entitlements the subscription granted.", ElementType: types.StringType, Computed: true},
				"renewal_sku_ids":      schema.ListAttribute{MarkdownDescription: "IDs of the SKUs the user will be subscribed to at renewal, or null.", ElementType: types.StringType, Computed: true},
				"current_period_start": computedString("Start of the current period."),
				"current_period_end":   computedString("End of the current period."),
				"status":               computedString("Status: " + subscriptionTypes.doc() + "."),
				"canceled_at":          computedString("When the subscription was canceled, or null."),
			}),
		},
	}
}

func (d *skuSubscriptionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m skuSubscriptionsDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	limit := int64(50)
	if !m.Limit.IsNull() {
		limit = m.Limit.ValueInt64()
	}
	all, err := fetchPages(discord.Page{Before: m.Before.ValueString(), After: m.After.ValueString()}, limit,
		func(p discord.Page) ([]discord.Subscription, error) {
			return d.client.ListSKUSubscriptions(ctx, m.SKUID.ValueString(), m.UserID.ValueString(), p)
		},
		func(s discord.Subscription) string { return s.ID })
	if err != nil {
		apiError(&resp.Diagnostics, "list SKU subscriptions", err)
		return
	}
	m.Subscriptions = []subscriptionModel{}
	for _, s := range all {
		renewal := types.ListNull(types.StringType)
		if s.RenewalSKUIDs != nil {
			renewal = stringListValue(ctx, s.RenewalSKUIDs, &resp.Diagnostics)
		}
		m.Subscriptions = append(m.Subscriptions, subscriptionModel{
			ID:                 types.StringValue(s.ID),
			UserID:             types.StringValue(s.UserID),
			SKUIDs:             stringListValue(ctx, s.SKUIDs, &resp.Diagnostics),
			EntitlementIDs:     stringListValue(ctx, s.EntitlementIDs, &resp.Diagnostics),
			RenewalSKUIDs:      renewal,
			CurrentPeriodStart: types.StringValue(s.CurrentPeriodStart),
			CurrentPeriodEnd:   types.StringValue(s.CurrentPeriodEnd),
			Status:             subscriptionTypes.name(s.Status),
			CanceledAt:         stringPtrValue(s.CanceledAt),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
