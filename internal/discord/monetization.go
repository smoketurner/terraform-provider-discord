package discord

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

// ListSKUs lists an application's SKUs.
func (c *Client) ListSKUs(ctx context.Context, applicationID string) ([]SKU, error) {
	var skus []SKU
	return skus, c.do(ctx, http.MethodGet, "/applications/"+applicationID+"/skus", nil, &skus)
}

// Page selects a page of a list paginated by ID. Empty and zero fields are
// not sent.
type Page struct {
	Before string
	After  string
	Limit  int
}

// EntitlementQuery filters an application's entitlements. ExcludeDeleted
// is sent only when not nil; Discord excludes deleted entitlements by
// default.
type EntitlementQuery struct {
	Page
	UserID         string
	GuildID        string
	SKUIDs         []string
	ExcludeEnded   bool
	ExcludeDeleted *bool
}

// ListEntitlements fetches one page of an application's entitlements.
func (c *Client) ListEntitlements(ctx context.Context, applicationID string, q EntitlementQuery) ([]Entitlement, error) {
	path := "/applications/" + applicationID + "/entitlements?limit=" + strconv.Itoa(q.Limit)
	if q.UserID != "" {
		path += "&user_id=" + q.UserID
	}
	if q.GuildID != "" {
		path += "&guild_id=" + q.GuildID
	}
	if len(q.SKUIDs) > 0 {
		path += "&sku_ids=" + strings.Join(q.SKUIDs, ",")
	}
	if q.ExcludeEnded {
		path += "&exclude_ended=true"
	}
	if q.ExcludeDeleted != nil {
		path += "&exclude_deleted=" + strconv.FormatBool(*q.ExcludeDeleted)
	}
	if q.Before != "" {
		path += "&before=" + q.Before
	}
	if q.After != "" {
		path += "&after=" + q.After
	}
	var entitlements []Entitlement
	return entitlements, c.do(ctx, http.MethodGet, path, nil, &entitlements)
}

// ListSKUSubscriptions fetches one page of a user's subscriptions to an SKU.
// Bots must filter by user.
func (c *Client) ListSKUSubscriptions(ctx context.Context, skuID, userID string, p Page) ([]Subscription, error) {
	path := "/skus/" + skuID + "/subscriptions?user_id=" + userID + "&limit=" + strconv.Itoa(p.Limit)
	if p.Before != "" {
		path += "&before=" + p.Before
	}
	if p.After != "" {
		path += "&after=" + p.After
	}
	var subs []Subscription
	return subs, c.do(ctx, http.MethodGet, path, nil, &subs)
}
