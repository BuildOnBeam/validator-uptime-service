package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Slack struct {
	webhookURL string
	client     *http.Client
}

// NewSlack returns a Slack notifier that posts to webhookURL. Pass an
// empty string for local/dev/ad-hoc runs that shouldn't notify; Post
// becomes a no-op in that case.
func NewSlack(webhookURL string) *Slack {
	return &Slack{
		webhookURL: webhookURL,
		client:     &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *Slack) Enabled() bool {
	return s != nil && s.webhookURL != ""
}

// Post sends text as a Slack message. Errors are returned to the caller —
// callers should log-and-continue, never abort a run because of a failed
// notification.
func (s *Slack) Post(text string) error {
	if !s.Enabled() {
		return nil
	}
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return fmt.Errorf("marshal slack payload: %w", err)
	}
	resp, err := s.client.Post(s.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post slack: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("slack returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
