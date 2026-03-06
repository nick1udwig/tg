package main

import (
	"encoding/json"
	"fmt"
)

type apiEnvelope struct {
	Ok          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
	Parameters  ResponseParams  `json:"parameters,omitempty"`
}

type ResponseParams struct {
	MigrateToChatID int64 `json:"migrate_to_chat_id,omitempty"`
	RetryAfter      int   `json:"retry_after,omitempty"`
}

type BotAPIError struct {
	Method      string
	ErrorCode   int
	Description string
}

func (e *BotAPIError) Error() string {
	if e == nil {
		return ""
	}
	if e.ErrorCode > 0 {
		return fmt.Sprintf("telegram %s failed (%d): %s", e.Method, e.ErrorCode, e.Description)
	}
	return fmt.Sprintf("telegram %s failed: %s", e.Method, e.Description)
}

type SendMessageRequest struct {
	ChatID              any
	Text                string
	ParseMode           string
	MessageThreadID     int64
	DirectMessagesTopic int64
	DisableNotification bool
	ProtectContent      bool
	AllowPaidBroadcast  bool
	ReplyToMessageID    int64
}

func (r SendMessageRequest) Params() map[string]any {
	params := map[string]any{
		"chat_id": r.ChatID,
		"text":    r.Text,
	}
	if r.ParseMode != "" {
		params["parse_mode"] = r.ParseMode
	}
	if r.MessageThreadID > 0 {
		params["message_thread_id"] = r.MessageThreadID
	}
	if r.DirectMessagesTopic > 0 {
		params["direct_messages_topic_id"] = r.DirectMessagesTopic
	}
	if r.DisableNotification {
		params["disable_notification"] = true
	}
	if r.ProtectContent {
		params["protect_content"] = true
	}
	if r.AllowPaidBroadcast {
		params["allow_paid_broadcast"] = true
	}
	if r.ReplyToMessageID > 0 {
		params["reply_parameters"] = map[string]any{
			"message_id": r.ReplyToMessageID,
		}
	}
	return params
}

type GetUpdatesRequest struct {
	Offset            *int64
	Limit             int
	TimeoutSeconds    int
	AllowedUpdates    []string
	HasAllowedUpdates bool
}

func (r GetUpdatesRequest) Params() map[string]any {
	params := map[string]any{}
	if r.Offset != nil {
		params["offset"] = *r.Offset
	}
	if r.Limit > 0 {
		params["limit"] = r.Limit
	}
	if r.TimeoutSeconds >= 0 {
		params["timeout"] = r.TimeoutSeconds
	}
	if r.HasAllowedUpdates {
		params["allowed_updates"] = r.AllowedUpdates
	}
	return params
}

type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type,omitempty"`
	Title    string `json:"title,omitempty"`
	Username string `json:"username,omitempty"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Date      int64  `json:"date,omitempty"`
	From      *User  `json:"from,omitempty"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text,omitempty"`
}

type CallbackQuery struct {
	ID              string   `json:"id"`
	From            *User    `json:"from,omitempty"`
	Message         *Message `json:"message,omitempty"`
	InlineMessageID string   `json:"inline_message_id,omitempty"`
	Data            string   `json:"data,omitempty"`
}

type Update struct {
	UpdateID          int64          `json:"update_id"`
	Message           *Message       `json:"message,omitempty"`
	EditedMessage     *Message       `json:"edited_message,omitempty"`
	ChannelPost       *Message       `json:"channel_post,omitempty"`
	EditedChannelPost *Message       `json:"edited_channel_post,omitempty"`
	CallbackQuery     *CallbackQuery `json:"callback_query,omitempty"`
}

type WebhookInfo struct {
	URL                  string `json:"url"`
	HasCustomCertificate bool   `json:"has_custom_certificate"`
	PendingUpdateCount   int    `json:"pending_update_count"`
	IPAddress            string `json:"ip_address,omitempty"`
	LastErrorDate        int64  `json:"last_error_date,omitempty"`
	LastErrorMessage     string `json:"last_error_message,omitempty"`
	MaxConnections       int    `json:"max_connections,omitempty"`
}
