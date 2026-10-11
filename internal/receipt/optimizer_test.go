package receipt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDecidePaymentReceipt(t *testing.T) {
	tests := []struct {
		name         string
		risk         Risk
		wantDecision string
		wantCalls    int
		wantImageID  string
	}{
		{name: "low risk is optimized", risk: RiskLow, wantDecision: "ready_to_serve", wantCalls: 1, wantImageID: "img_compact_42"},
		{name: "high risk waits for review", risk: RiskHigh, wantDecision: "review_required", wantCalls: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost {
					t.Fatalf("method = %s", r.Method)
				}
				if r.URL.Path != "/v1/image/compress" {
					t.Fatalf("path = %s", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Fatal("missing bearer authentication")
				}
				if r.Header.Get("Idempotency-Key") != "evt_42" {
					t.Fatal("missing event idempotency key")
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Fatal(err)
				}
				file, _, err := r.FormFile("image")
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				got, _ := io.ReadAll(file)
				if string(got) != "receipt-bytes" {
					t.Fatalf("image = %q", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true,"data":{"id":"img_compact_42"},"error":null,"metadata":{}}`))
			}))
			defer server.Close()

			client := &Client{BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client()}
			event := PaymentEvent{ID: "evt_42", ReceiptID: "receipt_42", Risk: tt.risk, OccurredAt: "2026-09-09T10:00:00Z"}
			note, err := Decide(context.Background(), client, event, "receipt.png", []byte("receipt-bytes"))
			if err != nil {
				t.Fatal(err)
			}
			if note.Decision != tt.wantDecision || note.ImageID != tt.wantImageID || calls != tt.wantCalls {
				t.Fatalf("notification = %#v, calls = %d", note, calls)
			}
		})
	}
}

func TestCompressRetriesRateLimit(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"data":null,"error":{"code":"","message":"retry later"},"metadata":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"data":{"id":"img_retry_1"},"error":null,"metadata":{}}`))
	}))
	defer server.Close()

	var waited time.Duration
	client := &Client{
		BaseURL: server.URL, APIKey: "test-key", HTTPClient: server.Client(), MaxRetries: 1,
		Sleep: func(_ context.Context, delay time.Duration) error { waited = delay; return nil },
	}
	id, err := client.Compress(context.Background(), "evt_retry", "receipt.png", []byte("image"))
	if err != nil {
		t.Fatal(err)
	}
	if id != "img_retry_1" || calls != 2 || waited != 3*time.Second {
		t.Fatalf("id = %q, calls = %d, waited = %s", id, calls, waited)
	}
}
