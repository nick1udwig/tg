package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultAPIBaseURL = "https://api.telegram.org"

type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

func NewClient(token, baseURL string, timeout time.Duration) (*Client, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		token:   token,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func (c *Client) endpoint(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", c.baseURL, c.token, method)
}

func (c *Client) call(ctx context.Context, method string, params map[string]any, out any) error {
	if params == nil {
		params = map[string]any{}
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal %s payload: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(method), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s request failed: %w", method, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", method, err)
	}

	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	if !envelope.Ok {
		return &BotAPIError{
			Method:      method,
			ErrorCode:   envelope.ErrorCode,
			Description: envelope.Description,
		}
	}
	if out == nil || len(envelope.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}
	return nil
}

func (c *Client) SendMessage(ctx context.Context, req SendMessageRequest) (Message, error) {
	var msg Message
	if err := c.call(ctx, "sendMessage", req.Params(), &msg); err != nil {
		return Message{}, err
	}
	return msg, nil
}

func (c *Client) GetUpdates(ctx context.Context, req GetUpdatesRequest) ([]Update, error) {
	var updates []Update
	if err := c.call(ctx, "getUpdates", req.Params(), &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

func (c *Client) DeleteWebhook(ctx context.Context, dropPending bool) (bool, error) {
	params := map[string]any{}
	if dropPending {
		params["drop_pending_updates"] = true
	}
	var ok bool
	if err := c.call(ctx, "deleteWebhook", params, &ok); err != nil {
		return false, err
	}
	return ok, nil
}

func (c *Client) GetWebhookInfo(ctx context.Context) (WebhookInfo, error) {
	var info WebhookInfo
	if err := c.call(ctx, "getWebhookInfo", nil, &info); err != nil {
		return WebhookInfo{}, err
	}
	return info, nil
}
