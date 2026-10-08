package discordtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/smoketurner/terraform-provider-discord/internal/discord"
)

// Message flags Create Message and Edit Message accept. Other flags in a
// create request are rejected, and edits ignore them as Discord does.
const (
	messageFlagIsVoiceMessage = 1 << 13
	createMessageFlags        = discord.MessageFlagSuppressEmbeds | discord.MessageFlagSuppressNotifications |
		messageFlagIsVoiceMessage | discord.MessageFlagIsComponentsV2
	editMessageFlags = discord.MessageFlagSuppressEmbeds | discord.MessageFlagIsComponentsV2
)

// errBadMessage is a 400 Invalid Form Body for a message request.
type errBadMessage struct {
	code int
	msg  string
}

func (e *errBadMessage) Error() string { return e.msg }

func badMessage(format string, args ...any) error {
	return &errBadMessage{code: 50035, msg: fmt.Sprintf(format, args...)}
}

// attachmentRequest is an entry of a request's attachments array. ID is a
// kept attachment's snowflake or a new file's files[n] index.
type attachmentRequest struct {
	ID          json.RawMessage `json:"id"`
	Filename    *string         `json:"filename"`
	Description *string         `json:"description"`
	IsSpoiler   *bool           `json:"is_spoiler"`
}

func (a attachmentRequest) id() string {
	return strings.Trim(string(a.ID), `"`)
}

// applyMessageParts mimics how Discord applies the flags, attachments,
// components, stickers and poll of a Create Message or Edit Message request
// to m, which content and embeds were already applied to.
func (s *Server) applyMessageParts(m *discord.Message, body map[string]json.RawMessage, create bool) error {
	if m.Poll != nil && !create {
		for _, key := range []string{"content", "embeds", "components", "attachments", "flags"} {
			if _, ok := body[key]; ok {
				return &errBadMessage{code: 50160, msg: "Cannot edit a message with a poll"}
			}
		}
	}
	if err := applyMessageFlags(m, body, create); err != nil {
		return err
	}
	if err := s.applyAttachments(m, body, create); err != nil {
		return err
	}
	if err := applyComponents(m, body); err != nil {
		return err
	}
	if create {
		if err := s.applyStickers(m, body); err != nil {
			return err
		}
		if err := applyPoll(m, body); err != nil {
			return err
		}
	}
	for i := range m.Embeds {
		resolveEmbedAttachments(&m.Embeds[i], m.Attachments)
	}
	if m.Flags&discord.MessageFlagSuppressEmbeds != 0 {
		m.Embeds = []discord.Embed{}
	}
	if m.Flags&discord.MessageFlagIsComponentsV2 != 0 &&
		(m.Content != "" || len(m.Embeds) > 0 || len(m.StickerItems) > 0 || m.Poll != nil) {
		return badMessage("a Components V2 message can contain only components")
	}
	if m.Content == "" && len(m.Embeds) == 0 && len(m.Attachments) == 0 && len(m.Components) == 0 &&
		len(m.StickerItems) == 0 && m.Poll == nil {
		return &errBadMessage{code: 50006, msg: "Cannot send an empty message"}
	}
	return nil
}

func applyMessageFlags(m *discord.Message, body map[string]json.RawMessage, create bool) error {
	if _, ok := body["flags"]; !ok {
		return nil
	}
	var flags int64
	set(body, "flags", &flags)
	if create {
		if flags&^createMessageFlags != 0 {
			return badMessage("flags %d cannot be set when creating a message", flags)
		}
		m.Flags = flags
		return nil
	}
	// Components V2 can be turned on but never off.
	keep := m.Flags & discord.MessageFlagIsComponentsV2
	m.Flags = m.Flags&^editMessageFlags | flags&editMessageFlags | keep
	return nil
}

// applyAttachments uploads the files[n] parts. On create every file is
// attached; on edit an attachments array lists every attachment to keep, and
// without one new files are appended.
func (s *Server) applyAttachments(m *discord.Message, body map[string]json.RawMessage, create bool) error {
	uploads := map[string]Upload{}
	for key, raw := range body {
		if n, ok := strings.CutPrefix(key, "files["); ok {
			var u Upload
			if err := json.Unmarshal(raw, &u); err != nil || !strings.HasSuffix(n, "]") {
				return badMessage("invalid file field %s", key)
			}
			uploads[strings.TrimSuffix(n, "]")] = u
		}
	}
	var reqs []attachmentRequest
	_, listed := body["attachments"]
	set(body, "attachments", &reqs)
	if !listed {
		if !create {
			for _, a := range m.Attachments {
				reqs = append(reqs, attachmentRequest{ID: json.RawMessage(strconv.Quote(a.ID))})
			}
		}
		for _, n := range slices.Sorted(maps.Keys(uploads)) {
			reqs = append(reqs, attachmentRequest{ID: json.RawMessage(n)})
		}
	}
	var out []discord.Attachment
	for _, req := range reqs {
		var a discord.Attachment
		if i := slices.IndexFunc(m.Attachments, func(a discord.Attachment) bool { return a.ID == req.id() }); i >= 0 && !create {
			a = m.Attachments[i]
		} else if u, ok := uploads[req.id()]; ok {
			delete(uploads, req.id())
			a = discord.Attachment{ID: s.newID(), Filename: u.Filename, ContentType: u.ContentType, Size: int64(len(u.Data))}
			if req.Filename != nil {
				a.Filename = *req.Filename
			}
			a.URL = fmt.Sprintf("https://cdn.discordapp.com/attachments/%s/%s/%s?ex=%x", m.ChannelID, a.ID, a.Filename, time.Now().Unix())
		} else {
			return badMessage("attachment %s is neither an attachment of the message nor an uploaded file", req.id())
		}
		if req.Description != nil {
			if len(*req.Description) > 1024 {
				return badMessage("attachment description must be at most 1024 characters")
			}
			a.Description = *req.Description
		}
		if req.IsSpoiler != nil {
			a.Flags &^= discord.AttachmentFlagIsSpoiler
			if *req.IsSpoiler {
				a.Flags |= discord.AttachmentFlagIsSpoiler
			}
		}
		out = append(out, a)
	}
	if len(uploads) > 0 && listed {
		return badMessage("every uploaded file must be listed in attachments")
	}
	if len(out) > 10 {
		return badMessage("a message can have at most 10 attachments")
	}
	if out == nil {
		out = []discord.Attachment{}
	}
	m.Attachments = out
	return nil
}

// applyComponents stores components as Discord does: components without an
// id get a generated one, and attachment:// media references resolve to the
// attachment's URL.
func applyComponents(m *discord.Message, body map[string]json.RawMessage) error {
	if _, ok := body["components"]; !ok {
		return nil
	}
	var comps []any
	set(body, "components", &comps)
	v2 := m.Flags&discord.MessageFlagIsComponentsV2 != 0
	if !v2 {
		if len(comps) > 5 {
			return badMessage("at most 5 action rows are allowed")
		}
		for _, c := range comps {
			if obj, ok := c.(map[string]any); !ok || obj["type"] != float64(1) {
				return badMessage("top-level components must be action rows without IS_COMPONENTS_V2")
			}
		}
	}
	nextID := 0
	if n := walkComponents(comps, func(map[string]any) {}); v2 && n > 40 {
		return badMessage("a message can have at most 40 components")
	}
	walkComponents(comps, func(c map[string]any) {
		nextID++
		if id, ok := c["id"].(float64); !ok || id == 0 {
			c["id"] = nextID
		}
	})
	if err := resolveMedia(comps, m.Attachments); err != nil {
		return err
	}
	m.Components = nil
	for _, c := range comps {
		raw, _ := json.Marshal(c)
		m.Components = append(m.Components, raw)
	}
	return nil
}

// walkComponents calls fn for every component, nested ones included, and
// returns how many there are.
func walkComponents(v any, fn func(map[string]any)) int {
	switch c := v.(type) {
	case []any:
		n := 0
		for _, e := range c {
			n += walkComponents(e, fn)
		}
		return n
	case map[string]any:
		n := 0
		if _, ok := c["type"]; ok {
			fn(c)
			n = 1
		}
		return n + walkComponents(c["components"], fn) + walkComponents(c["accessory"], fn)
	default:
		return 0
	}
}

// resolveMedia replaces attachment://<filename> URLs anywhere in v.
func resolveMedia(v any, atts []discord.Attachment) error {
	switch c := v.(type) {
	case []any:
		for _, e := range c {
			if err := resolveMedia(e, atts); err != nil {
				return err
			}
		}
	case map[string]any:
		for k, e := range c {
			if s, ok := e.(string); ok && k == "url" && strings.HasPrefix(s, "attachment://") {
				a, ok := attachmentNamed(atts, strings.TrimPrefix(s, "attachment://"))
				if !ok {
					return badMessage("%s does not name an attachment of the message", s)
				}
				c[k] = a.URL
				continue
			}
			if err := resolveMedia(e, atts); err != nil {
				return err
			}
		}
	}
	return nil
}

func attachmentNamed(atts []discord.Attachment, name string) (discord.Attachment, bool) {
	i := slices.IndexFunc(atts, func(a discord.Attachment) bool { return a.Filename == name })
	if i < 0 {
		return discord.Attachment{}, false
	}
	return atts[i], true
}

func resolveEmbedAttachments(e *discord.Embed, atts []discord.Attachment) {
	resolve := func(url *string) {
		if name, ok := strings.CutPrefix(*url, "attachment://"); ok {
			if a, ok := attachmentNamed(atts, name); ok {
				*url = a.URL
			}
		}
	}
	if e.Image != nil {
		resolve(&e.Image.URL)
	}
	if e.Thumbnail != nil {
		resolve(&e.Thumbnail.URL)
	}
	if e.Footer != nil {
		resolve(&e.Footer.IconURL)
	}
	if e.Author != nil {
		resolve(&e.Author.IconURL)
	}
}

func (s *Server) applyStickers(m *discord.Message, body map[string]json.RawMessage) error {
	var ids []string
	set(body, "sticker_ids", &ids)
	if len(ids) > 3 {
		return badMessage("a message can have at most 3 stickers")
	}
	for _, id := range ids {
		var found *discord.Sticker
		for _, stickers := range s.stickers {
			if st, ok := stickers[id]; ok {
				found = st
			}
		}
		if found == nil {
			return &errBadMessage{code: 50081, msg: "Invalid Sticker Sent"}
		}
		m.StickerItems = append(m.StickerItems, discord.StickerItem{ID: found.ID, Name: found.Name, FormatType: int64(found.FormatType)})
	}
	return nil
}

func applyPoll(m *discord.Message, body map[string]json.RawMessage) error {
	var req *struct {
		Question         discord.PollMedia    `json:"question"`
		Answers          []discord.PollAnswer `json:"answers"`
		Duration         *int64               `json:"duration"`
		AllowMultiselect bool                 `json:"allow_multiselect"`
	}
	set(body, "poll", &req)
	if req == nil {
		return nil
	}
	duration := int64(24)
	if req.Duration != nil {
		duration = *req.Duration
	}
	switch {
	case len(req.Question.Text) < 1 || len(req.Question.Text) > 300:
		return badMessage("poll question must be 1-300 characters")
	case len(req.Answers) < 1 || len(req.Answers) > 10:
		return badMessage("a poll must have 1-10 answers")
	case duration < 1 || duration > 768:
		return badMessage("poll duration must be 1-768 hours")
	}
	posted, err := time.Parse(time.RFC3339Nano, m.Timestamp)
	if err != nil {
		return errors.New("message has no timestamp")
	}
	expiry := posted.Add(time.Duration(duration) * time.Hour).Format(time.RFC3339Nano)
	poll := &discord.Poll{Question: req.Question, Expiry: &expiry, AllowMultiselect: req.AllowMultiselect, LayoutType: 1}
	for i, a := range req.Answers {
		if len(a.PollMedia.Text) < 1 || len(a.PollMedia.Text) > 55 {
			return badMessage("poll answers must be 1-55 characters")
		}
		// Discord returns the name of a custom emoji with its ID.
		if e := a.PollMedia.Emoji; e != nil && e.ID != nil && e.Name == nil {
			name := "emoji_" + *e.ID
			e.Name = &name
		}
		poll.Answers = append(poll.Answers, discord.PollAnswer{AnswerID: int64(i + 1), PollMedia: a.PollMedia})
	}
	m.Poll = poll
	return nil
}

func writeMessageError(w http.ResponseWriter, err error) {
	var bad *errBadMessage
	if errors.As(err, &bad) {
		writeError(w, http.StatusBadRequest, bad.code, bad.msg)
		return
	}
	writeError(w, http.StatusInternalServerError, 0, err.Error())
}
