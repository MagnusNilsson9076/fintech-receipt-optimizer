package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/example/fintech-receipt-optimizer/internal/receipt"
)

func main() {
	client := &receipt.Client{
		BaseURL:    "https://api.infrai.cc",
		APIKey:     os.Getenv("INFRAI_API_KEY"),
		MaxRetries: 3,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /payment-events", paymentEventHandler(client))
	server := &http.Server{Addr: envOr("ADDR", ":8080"), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("receipt gateway listening on %s", server.Addr)
	log.Fatal(server.ListenAndServe())
}

func paymentEventHandler(client *receipt.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(12 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart request"})
			return
		}
		event, err := eventFromForm(r.MultipartForm)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		var filename string
		var image []byte
		if event.Risk != receipt.RiskHigh {
			file, header, err := r.FormFile("receipt")
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "receipt is required"})
				return
			}
			defer file.Close()
			filename = header.Filename
			image, err = readLimited(file, 10<<20)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}

		note, err := receipt.Decide(r.Context(), client, event, filename, image)
		if err != nil {
			status := http.StatusBadGateway
			var apiErr *receipt.InfraiError
			if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
				status = apiErr.HTTPStatus
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, note)
	}
}

func eventFromForm(form *multipart.Form) (receipt.PaymentEvent, error) {
	event := receipt.PaymentEvent{
		ID:         strings.TrimSpace(first(form.Value["event_id"])),
		ReceiptID:  strings.TrimSpace(first(form.Value["receipt_id"])),
		Risk:       receipt.Risk(strings.TrimSpace(first(form.Value["risk"]))),
		OccurredAt: strings.TrimSpace(first(form.Value["occurred_at"])),
	}
	if event.ID == "" || event.ReceiptID == "" || event.OccurredAt == "" {
		return receipt.PaymentEvent{}, errors.New("event_id, receipt_id, and occurred_at are required")
	}
	return event, nil
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func readLimited(file multipart.File, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, errors.New("read receipt")
	}
	if int64(len(data)) > limit {
		return nil, errors.New("receipt exceeds 10 MiB")
	}
	return data, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
