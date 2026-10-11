package receipt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Risk string

const (
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

type PaymentEvent struct {
	ID         string `json:"id"`
	ReceiptID  string `json:"receipt_id"`
	Risk       Risk   `json:"risk"`
	OccurredAt string `json:"occurred_at"`
}

type AuditNotification struct {
	EventID    string `json:"event_id"`
	ReceiptID  string `json:"receipt_id"`
	Decision   string `json:"decision"`
	ImageID    string `json:"image_id,omitempty"`
	OccurredAt string `json:"occurred_at"`
}

type InfraiError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *InfraiError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	MaxRetries int
	Sleep      func(context.Context, time.Duration) error
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    *apiError       `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type compressedImage struct {
	ID string `json:"id"`
}

func (c *Client) Compress(ctx context.Context, eventID, filename string, image []byte) (string, error) {
	if c.APIKey == "" {
		return "", errors.New("INFRAI_API_KEY is required")
	}
	if eventID == "" || filename == "" || len(image) == 0 {
		return "", errors.New("event ID, filename, and image are required")
	}

	for attempt := 0; ; attempt++ {
		body, contentType, err := imageBody(filename, image)
		if err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/v1/image/compress", body)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Idempotency-Key", eventID)

		res, err := c.httpClient().Do(req)
		if err != nil {
			return "", fmt.Errorf("send compression request: %w", err)
		}
		payload, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			return "", fmt.Errorf("read compression response: %w", readErr)
		}

		var env envelope
		if err := json.Unmarshal(payload, &env); err != nil {
			return "", fmt.Errorf("decode compression envelope (HTTP %d): %w", res.StatusCode, err)
		}
		if res.StatusCode == http.StatusTooManyRequests && attempt < c.MaxRetries {
			if err := c.sleep(ctx, retryDelay(res.Header.Get("Retry-After"), attempt)); err != nil {
				return "", err
			}
			continue
		}
		if !env.OK {
			apiErr := env.Error
			if apiErr == nil {
				apiErr = &apiError{Message: "request rejected"}
			}
			return "", &InfraiError{Code: apiErr.Code, Message: apiErr.Message, HTTPStatus: res.StatusCode}
		}
		if res.StatusCode >= http.StatusInternalServerError {
			return "", fmt.Errorf("compression transport status: %d", res.StatusCode)
		}

		var result compressedImage
		if err := json.Unmarshal(env.Data, &result); err != nil {
			return "", fmt.Errorf("decode compressed image: %w", err)
		}
		if result.ID == "" {
			return "", errors.New("compression response omitted image id")
		}
		return result.ID, nil
	}
}

func Decide(ctx context.Context, client *Client, event PaymentEvent, filename string, image []byte) (AuditNotification, error) {
	note := AuditNotification{EventID: event.ID, ReceiptID: event.ReceiptID, OccurredAt: event.OccurredAt}
	if event.Risk == RiskHigh {
		note.Decision = "review_required"
		return note, nil
	}
	if event.Risk != RiskLow && event.Risk != RiskMedium {
		return AuditNotification{}, errors.New("risk must be low, medium, or high")
	}
	id, err := client.Compress(ctx, event.ID, filename, image)
	if err != nil {
		return AuditNotification{}, err
	}
	note.Decision = "ready_to_serve"
	note.ImageID = id
	return note, nil
}

func imageBody(filename string, image []byte) (io.Reader, string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("image", filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(image); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return bytes.NewReader(body.Bytes()), w.FormDataContentType(), nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c *Client) sleep(ctx context.Context, delay time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryDelay(value string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Second << attempt
}
