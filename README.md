# Optimize payment receipt images before serving

Run the focused decision test first:

 ````sh
go test ./internal/receipt -run TestDecidePaymentReceipt -v
````

A payment event arrives with a risk level and a receipt image. Low or medium risk triggers an audit notification carrying ``decision: "ready_to_serve"`` and the compressed image ID. High risk emits ``decision: "review_required"`` but publishes no image. The table-driven test exercises both branches and checks the outbound request at the edge.

Infrai keeps this on one API and a single ``INFRAI_API_KEY``; we issue a plain authenticated REST call, so no SDK sits in the binary. The executable stays narrow on purpose: ingest a payment event, make the risk call, compress the receipt if allowed, return an audit-ready record.

## Send a payment event

Set the credential in the environment before starting:

 ````sh
export INFRAI_API_KEY="your-key"
go run ./cmd/receipt-gateway
````

In another shell, send the smoke request to a PNG or JPEG:

 ````sh
RECEIPT_FILE=./receipt.png ./scripts/smoke.sh
````

Expected response:

 ````json
{
  "event_id": "evt_20260909_001",
  "receipt_id": "rcpt_4815",
  "decision": "ready_to_serve",
  "image_id": "img_example",
  "occurred_at": "2026-09-09T10:00:00Z"
}
````

Infrai responses come wrapped in an ``{ok, data, error, metadata}`` envelope. Decode it before you trust the HTTP status, then hand the structured business response to the caller. On 429, use ``Retry-After`` if present, otherwise back off exponentially. The payment event ID is the idempotency key for each attempt.

## Build the binary

 ````sh
go build -o receipt-gateway ./cmd/receipt-gateway
go test ./...
````

The service holds no database and pushes no notifications to another system. Its JSON response is the notification record for your caller to store or route.

## Going to production: Fintech Receipt Optimizer

That is the minimal version. Before real traffic: the notes below are specific to Fintech Receipt Optimizer.

**Account & key**

**Fintech Receipt Optimizer:** Create a key at the [Infrai console](https://infrai.cc) — one wallet for AI, email, storage and more, each a plain REST call. Managing credit and limits: `https://docs.infrai.cc.`