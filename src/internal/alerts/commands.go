package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type tgUpdate struct {
	UpdateID int `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

type tgUpdatesResponse struct {
	OK     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}

// CommandHandler polls Telegram for bot commands and dispatches them.
// Currently handles /status → sends an immediate digest to the requesting chat.
type CommandHandler struct {
	token  string
	digest *DailyDigest
	client *http.Client
	offset int
}

// NewCommandHandler constructs a handler that polls for commands and uses
// digest to respond to /status.
func NewCommandHandler(token string, digest *DailyDigest) *CommandHandler {
	return &CommandHandler{
		token:  token,
		digest: digest,
		client: &http.Client{Timeout: 35 * time.Second},
	}
}

// Run starts the long-poll loop and blocks until ctx is cancelled.
func (h *CommandHandler) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		updates, err := h.poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("telegram poll: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			h.offset = u.UpdateID + 1
			if u.Message == nil {
				continue
			}
			cmd := strings.ToLower(strings.Fields(u.Message.Text)[0])
			// Strip @botname suffix if present
			if idx := strings.Index(cmd, "@"); idx >= 0 {
				cmd = cmd[:idx]
			}
			if cmd == "/status" {
				h.digest.SendNow()
			}
		}
	}
}

func (h *CommandHandler) poll(ctx context.Context) ([]tgUpdate, error) {
	url := fmt.Sprintf(
		"https://api.telegram.org/bot%s/getUpdates?timeout=30&offset=%d",
		h.token, h.offset,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var tgResp tgUpdatesResponse
	if err := json.Unmarshal(body, &tgResp); err != nil {
		return nil, fmt.Errorf("parse updates: %w", err)
	}
	if !tgResp.OK {
		return nil, fmt.Errorf("telegram API not ok")
	}
	return tgResp.Result, nil
}
