package discord

// Channel types managed by the provider.
const (
	ChannelTypeText         = 0
	ChannelTypeVoice        = 2
	ChannelTypeCategory     = 4
	ChannelTypeAnnouncement = 5
	ChannelTypeStage        = 13
	ChannelTypeForum        = 15
	ChannelTypeMedia        = 16
)

// Thread types.
const (
	ChannelTypeAnnouncementThread = 10
	ChannelTypePublicThread       = 11
	ChannelTypePrivateThread      = 12
)

// Permission overwrite target types.
const (
	OverwriteTypeRole   = 0
	OverwriteTypeMember = 1
)

// Channel flags managed by the provider.
const (
	// ChannelFlagPinned pins a thread to the top of its forum or media
	// channel. Archiving the thread clears it.
	ChannelFlagPinned = 1 << 1
	// ChannelFlagRequireTag requires forum and media posts to have a tag.
	ChannelFlagRequireTag = 1 << 4
	// ChannelFlagHideMediaDownloadOptions hides the download options on a
	// media channel's embedded media.
	ChannelFlagHideMediaDownloadOptions = 1 << 15
)

// Guild is a Discord server.
type Guild struct {
	ID                          string   `json:"id"`
	Name                        string   `json:"name"`
	Icon                        *string  `json:"icon"`
	Description                 *string  `json:"description"`
	OwnerID                     string   `json:"owner_id"`
	AFKChannelID                *string  `json:"afk_channel_id"`
	AFKTimeout                  int64    `json:"afk_timeout"`
	VerificationLevel           int64    `json:"verification_level"`
	DefaultMessageNotifications int64    `json:"default_message_notifications"`
	ExplicitContentFilter       int64    `json:"explicit_content_filter"`
	Features                    []string `json:"features"`
	SystemChannelID             *string  `json:"system_channel_id"`
	SystemChannelFlags          int64    `json:"system_channel_flags"`
	RulesChannelID              *string  `json:"rules_channel_id"`
	PublicUpdatesChannelID      *string  `json:"public_updates_channel_id"`
	SafetyAlertsChannelID       *string  `json:"safety_alerts_channel_id"`
	PreferredLocale             string   `json:"preferred_locale"`
	PremiumTier                 int64    `json:"premium_tier"`
	PremiumProgressBarEnabled   bool     `json:"premium_progress_bar_enabled"`
	Roles                       []Role   `json:"roles,omitempty"`
}

// RoleColors are a role's colors. Secondary and tertiary colors require the
// ENHANCED_ROLE_COLORS guild feature.
type RoleColors struct {
	PrimaryColor   int64  `json:"primary_color"`
	SecondaryColor *int64 `json:"secondary_color"`
	TertiaryColor  *int64 `json:"tertiary_color"`
}

// Role is a guild role. Color is deprecated in requests in favor of Colors
// but is still returned.
type Role struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Color        int64       `json:"color"`
	Colors       *RoleColors `json:"colors,omitempty"`
	Hoist        bool        `json:"hoist"`
	Icon         *string     `json:"icon"`
	UnicodeEmoji *string     `json:"unicode_emoji"`
	Position     int64       `json:"position"`
	Permissions  string      `json:"permissions"`
	Managed      bool        `json:"managed"`
	Mentionable  bool        `json:"mentionable"`
}

// Overwrite is a channel permission overwrite for a role or member.
type Overwrite struct {
	ID    string `json:"id"`
	Type  int    `json:"type"`
	Allow string `json:"allow"`
	Deny  string `json:"deny"`
}

// ForumTag is a tag that can be applied to forum or media channel posts.
type ForumTag struct {
	ID        string  `json:"id,omitempty"`
	Name      string  `json:"name"`
	Moderated bool    `json:"moderated"`
	EmojiID   *string `json:"emoji_id"`
	EmojiName *string `json:"emoji_name"`
}

// DefaultReaction is the emoji shown on forum posts by default.
type DefaultReaction struct {
	EmojiID   *string `json:"emoji_id"`
	EmojiName *string `json:"emoji_name"`
}

// Channel is a guild channel.
type Channel struct {
	ID                            string           `json:"id"`
	Type                          int              `json:"type"`
	GuildID                       string           `json:"guild_id"`
	Position                      int64            `json:"position"`
	PermissionOverwrites          []Overwrite      `json:"permission_overwrites"`
	Name                          string           `json:"name"`
	Topic                         *string          `json:"topic"`
	NSFW                          bool             `json:"nsfw"`
	RateLimitPerUser              int64            `json:"rate_limit_per_user"`
	Bitrate                       int64            `json:"bitrate"`
	UserLimit                     int64            `json:"user_limit"`
	ParentID                      *string          `json:"parent_id"`
	RTCRegion                     *string          `json:"rtc_region"`
	VideoQualityMode              int64            `json:"video_quality_mode,omitempty"`
	DefaultAutoArchiveDuration    int64            `json:"default_auto_archive_duration"`
	Flags                         int64            `json:"flags"`
	AvailableTags                 []ForumTag       `json:"available_tags"`
	DefaultReactionEmoji          *DefaultReaction `json:"default_reaction_emoji"`
	DefaultThreadRateLimitPerUser int64            `json:"default_thread_rate_limit_per_user"`
	DefaultSortOrder              *int64           `json:"default_sort_order"`
	DefaultForumLayout            int64            `json:"default_forum_layout"`
}

// ThreadMetadata holds the fields specific to threads. Invitable is only
// present on private threads.
type ThreadMetadata struct {
	Archived            bool  `json:"archived"`
	AutoArchiveDuration int64 `json:"auto_archive_duration"`
	Locked              bool  `json:"locked"`
	Invitable           *bool `json:"invitable,omitempty"`
}

// Thread is a thread in a text or announcement channel, or a post in a forum
// or media channel. ParentID is the channel it was created in.
type Thread struct {
	ID               string          `json:"id"`
	Type             int             `json:"type"`
	GuildID          string          `json:"guild_id"`
	ParentID         *string         `json:"parent_id"`
	OwnerID          string          `json:"owner_id"`
	Name             string          `json:"name"`
	RateLimitPerUser int64           `json:"rate_limit_per_user"`
	Flags            int64           `json:"flags"`
	AppliedTags      []string        `json:"applied_tags"`
	ThreadMetadata   *ThreadMetadata `json:"thread_metadata"`
}

// PositionUpdate moves a role or channel to a new position.
type PositionUpdate struct {
	ID       string `json:"id"`
	Position int64  `json:"position"`
}

// User is a Discord user.
type User struct {
	ID            string  `json:"id"`
	Username      string  `json:"username"`
	Discriminator string  `json:"discriminator"`
	GlobalName    *string `json:"global_name"`
	Avatar        *string `json:"avatar"`
	Bot           bool    `json:"bot"`
}

// Member is a user's membership in a guild. CommunicationDisabledUntil is
// when the member's timeout ends; null or a time in the past means none.
type Member struct {
	User                       *User    `json:"user"`
	Nick                       *string  `json:"nick"`
	Roles                      []string `json:"roles"`
	JoinedAt                   string   `json:"joined_at"`
	CommunicationDisabledUntil *string  `json:"communication_disabled_until"`
}

// Ban is a user's ban from a guild. Reason is the audit log reason sent with
// the request that created the ban.
type Ban struct {
	Reason *string `json:"reason"`
	User   *User   `json:"user"`
}

// Webhook is a channel webhook.
type Webhook struct {
	ID        string  `json:"id"`
	Type      int     `json:"type"`
	GuildID   string  `json:"guild_id"`
	ChannelID string  `json:"channel_id"`
	Name      *string `json:"name"`
	Avatar    *string `json:"avatar"`
	Token     string  `json:"token"`
}

// InviteChannel is the partial channel included in an invite.
type InviteChannel struct {
	ID string `json:"id"`
}

// Invite is a channel invite with its metadata, as returned by the Get
// Channel Invites and Create Channel Invite endpoints.
type Invite struct {
	Code      string         `json:"code"`
	Channel   *InviteChannel `json:"channel"`
	MaxAge    int64          `json:"max_age"`
	MaxUses   int64          `json:"max_uses"`
	Uses      int64          `json:"uses"`
	Temporary bool           `json:"temporary"`
	CreatedAt string         `json:"created_at"`
	ExpiresAt *string        `json:"expires_at"`
}

// EmbedFooter is the footer of an embed.
type EmbedFooter struct {
	Text    string `json:"text"`
	IconURL string `json:"icon_url,omitempty"`
}

// EmbedMedia is an image or thumbnail of an embed.
type EmbedMedia struct {
	URL string `json:"url"`
}

// EmbedAuthor is the author of an embed.
type EmbedAuthor struct {
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	IconURL string `json:"icon_url,omitempty"`
}

// EmbedField is a name/value field of an embed.
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// Embed is rich content attached to a message.
type Embed struct {
	// Type is "rich" for embeds sent by bots; Discord adds other types, such
	// as "article", when it unfurls links in the content.
	Type        string       `json:"type,omitempty"`
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	URL         string       `json:"url,omitempty"`
	Color       *int64       `json:"color,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
	Image       *EmbedMedia  `json:"image,omitempty"`
	Thumbnail   *EmbedMedia  `json:"thumbnail,omitempty"`
	Author      *EmbedAuthor `json:"author,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
}

// Message is a channel message.
type Message struct {
	ID        string  `json:"id"`
	ChannelID string  `json:"channel_id"`
	Author    *User   `json:"author"`
	Content   string  `json:"content"`
	Embeds    []Embed `json:"embeds"`
	Pinned    bool    `json:"pinned"`
}

// Emoji is a custom guild emoji.
type Emoji struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Roles    []string `json:"roles"`
	Managed  bool     `json:"managed"`
	Animated bool     `json:"animated"`
}

// WidgetSettings are a guild's widget settings.
type WidgetSettings struct {
	Enabled   bool    `json:"enabled"`
	ChannelID *string `json:"channel_id"`
}

// WelcomeScreen is the screen shown to new members of a Community guild.
// Whether it is enabled is the guild's WELCOME_SCREEN_ENABLED feature.
type WelcomeScreen struct {
	Description     *string                `json:"description"`
	WelcomeChannels []WelcomeScreenChannel `json:"welcome_channels"`
}

// WelcomeScreenChannel is a channel linked from the welcome screen.
type WelcomeScreenChannel struct {
	ChannelID   string  `json:"channel_id"`
	Description string  `json:"description"`
	EmojiID     *string `json:"emoji_id"`
	EmojiName   *string `json:"emoji_name"`
}

// Onboarding is a guild's onboarding configuration.
type Onboarding struct {
	GuildID           string             `json:"guild_id"`
	Prompts           []OnboardingPrompt `json:"prompts"`
	DefaultChannelIDs []string           `json:"default_channel_ids"`
	Enabled           bool               `json:"enabled"`
	Mode              int64              `json:"mode"`
}

// OnboardingPrompt is a question shown during onboarding.
type OnboardingPrompt struct {
	ID           string                   `json:"id"`
	Type         int64                    `json:"type"`
	Options      []OnboardingPromptOption `json:"options"`
	Title        string                   `json:"title"`
	SingleSelect bool                     `json:"single_select"`
	Required     bool                     `json:"required"`
	InOnboarding bool                     `json:"in_onboarding"`
}

// OnboardingPromptOption is an answer to an onboarding prompt as Discord
// returns it. Requests set the emoji with emoji_id, emoji_name and
// emoji_animated instead of the emoji object.
type OnboardingPromptOption struct {
	ID          string       `json:"id"`
	ChannelIDs  []string     `json:"channel_ids"`
	RoleIDs     []string     `json:"role_ids"`
	Emoji       *PromptEmoji `json:"emoji"`
	Title       string       `json:"title"`
	Description *string      `json:"description"`
}

// PromptEmoji is the emoji of an onboarding prompt option.
type PromptEmoji struct {
	ID       *string `json:"id"`
	Name     *string `json:"name"`
	Animated bool    `json:"animated"`
}
