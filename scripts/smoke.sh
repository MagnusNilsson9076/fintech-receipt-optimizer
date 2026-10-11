#!/bin/sh
set -eu

: "${RECEIPT_FILE:?set RECEIPT_FILE to a local image}"

curl --fail-with-body --request POST http://localhost:8080/payment-events \
  --form event_id=evt_20260909_001 \
  --form receipt_id=rcpt_4815 \
  --form risk=low \
  --form occurred_at=2026-09-09T10:00:00Z \
  --form "receipt=@${RECEIPT_FILE}"
