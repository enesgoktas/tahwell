# Tahwell (تحويل)

> **Universal Integration Gateway for Logistics & Supply Chains.**  
> *Transform it well.*

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://golang.org)
[![Build Status](https://img.shields.io/badge/Build-Passing-brightgreen?style=flat)]()
[![Tests](https://img.shields.io/badge/Tests-100%25_Passing-success?style=flat)]()

---

## 📖 What is Tahwell?

In modern logistics and manufacturing, companies suffer from **integration fragmentation**:
- **Shippers / Manufacturers:** Operate legacy ERPs (SAP, Oracle, NetWeaver) that communicate via enterprise SOAP/XML or SFTP.
- **Logistics Providers & 3PLs:** Modern carriers that expect structured JSON REST APIs.

Connecting every manufacturer to every carrier requires **months of bespoke point-to-point integration code** ($N \times M$ complexity).

**Tahwell** solves this by acting as an independent, lightweight, high-throughput **Reverse Integration Gateway**:
1. Ingests transport orders in the shipper's native protocol (e.g. SOAP/XML).
2. Validates invariants against a technology-neutral **Canonical Transport Model**.
3. Transforms and enriches payloads via **declarative YAML mapping rules** (unit conversions, date formatting, facility lookups).
4. Dispatches the order to the target carrier's REST API.
5. Translates the carrier's confirmation back into the shipper's native protocol (e.g. SOAP Response).
6. Records an immutable **Event Ledger** (audit log) with nanosecond precision for complete observability.

---

## 🏗️ Architecture

```
                                  TAHWELL GATEWAY
┌──────────────────┐        ┌────────────────────────────────────────────────────────┐        ┌──────────────────┐
│  SHIPPER (ERP)   │        │                                                        │        │  CARRIER (3PL)   │
│                  │        │  1. Inbound Adapter                                    │        │                  │
│  Enterprise ERP  │  SOAP  │     - Parse & Validate XML Envelope                    │  JSON  │  Logistics API   │
│  (SAP / Oracle)  ├───────►│                                                        ├───────►│  (Modern REST)   │
│                  │        │  2. Canonical Normalization                            │        │                  │
│                  │◄───────┤     - pkg/canonical (Golden Record)                    │◄───────┤                  │
│                  │  SOAP  │                                                        │  JSON  │                  │
└──────────────────┘  Resp  │  3. Declarative Engine                                 │  Resp  └──────────────────┘
                            │     - Unit convert (Ton -> Kg)                         │
                            │     - Date parse (DD.MM.YYYY -> ISO8601 UTC)           │
                            │     - Facility lookup (PLANT-01 -> Address)            │
                            │                                                        │
                            │  4. Outbound Dispatcher                                │
                            │     - HTTP Client with Auth & Timeout                  │
                            │                                                        │
                            │  5. Event Ledger (SQLite)                              │
                            │     - Immutable Audit Trail & Metrics                  │
                            └────────────────────────────────────────────────────────┘
```

---

## ⚡ Quickstart

### Prerequisites
- Go 1.22+ installed
- `make` (optional)

### 1. Build & Run
```bash
git clone https://github.com/goktasenes/tahwell.git
cd tahwell

# Run all tests with race detector
make test

# Build and start server
make run
```

Server starts on `http://127.0.0.1:8080`:
- **Health Check:** `GET /health`
- **Inbound Orders Endpoint (Format-Agnostic):** `POST /api/v1/inbound/orders`
- **Inbound Orders with Profile:** `POST /api/v1/inbound/orders/{profile_id}`
- **Inbound Legacy SOAP Endpoint:** `POST /api/v1/inbound/orders/soap`
- **Carrier Webhook Endpoint (Bi-directional):** `POST /api/v1/webhooks/carrier`
- **Carrier Webhook with Profile:** `POST /api/v1/webhooks/carrier/{profile_id}`
- **Audit Ledger Endpoint:** `GET /api/v1/ledger/events`

---

## 🧪 Testing the Live Gateway

### 1. Send an Inbound Order (JSON or SOAP):

**Format-Agnostic JSON Order:**
```bash
curl -X POST http://127.0.0.1:8080/api/v1/inbound/orders \
  -H "Content-Type: application/json" \
  -d '{
    "reference_id": "ORD-90412",
    "pickup": {
      "facility_code": "PLANT-01",
      "scheduled_at": "2026-11-20T09:00:00Z"
    },
    "delivery": {
      "company_name": "Distribution Hub North",
      "address": "Industrial Zone 4th Ave No:12"
    },
    "cargo": {
      "package_type": "PALLET",
      "quantity": 24,
      "gross_weight_kg": 18500
    }
  }'
```

**Enterprise SOAP/XML Order:**
```bash
curl -X POST http://127.0.0.1:8080/api/v1/inbound/orders/soap \
  -H "Content-Type: text/xml; charset=utf-8" \
  -d @testdata/sample_soap_order.xml
```

### 2. Ingest Carrier Status Webhooks (Bi-directional Tracking):
```bash
curl -X POST http://127.0.0.1:8080/api/v1/webhooks/carrier \
  -H "Content-Type: application/json" \
  -d '{
    "order_reference_id": "ORD-90412",
    "carrier_trip_code": "CARRIER-TRIP-9988",
    "status": "DELIVERED",
    "timestamp": "2026-11-20T16:45:00Z",
    "location": "Distribution Hub North",
    "pod_url": "https://storage.carrier.example/pod/receipt_9988.pdf",
    "notes": "Signed by warehouse supervisor"
  }'
```

### 3. Inspect the Audit Ledger:
```bash
curl -s http://127.0.0.1:8080/api/v1/ledger/events | jq .
```

---

## 📂 Project Structure

```text
tahwell/
├── cmd/
│   └── tahwell/
│       └── main.go              # Application entry point & graceful shutdown
├── pkg/
│   ├── canonical/               # Technology-neutral Canonical Order & Status schemas
│   │   ├── order.go             # Canonical TransportOrder model
│   │   ├── order_test.go
│   │   ├── status.go            # Canonical StatusEvent milestone model
│   │   └── status_test.go
│   ├── mapping/                 # Declarative schema transformation engine & parsers
│   │   ├── parser.go            # Universal XML/JSON codec & content sniffer
│   │   ├── parser_test.go
│   │   ├── rule.go              # Mapping rule and spec definitions
│   │   ├── engine.go            # AST & path transformation executor
│   │   └── engine_test.go
│   └── profile/                 # Integration profile registry (multi-tenant mapping)
│       ├── profile.go
│       └── profile_test.go
├── internal/
│   ├── adapter/
│   │   ├── soap/                # Enterprise SOAP/XML inbound adapter
│   │   │   ├── soap.go
│   │   │   └── soap_test.go
│   │   └── rest/                # Carrier REST JSON outbound & generic dispatcher
│   │       ├── rest.go
│   │       └── rest_test.go
│   ├── ledger/                  # SQLite-backed immutable event audit ledger
│   │   ├── ledger.go
│   │   └── ledger_test.go
│   └── server/                  # HTTP multiplexer and integration handlers
│       ├── server.go            # Format-agnostic orders & bi-directional webhook router
│       └── server_test.go       # End-to-end integration test suite
├── testdata/
│   ├── sample_soap_order.xml    # Mock SOAP XML order
│   └── soap_to_canonical.yaml   # Declarative mapping specification
├── Makefile                     # Build & test tooling
├── go.mod
└── go.sum
```

---

## 🛡️ Senior Engineering Design Choices

1. **Zero External Heavyweight Frameworks:** Utilizes Go 1.22+ standard library `http.ServeMux` for method routing and pattern matching. Zero bloat, maximum performance.
2. **CGO-Free Embedded SQLite:** Uses `modernc.org/sqlite` (pure Go transcompiled SQLite) ensuring static binaries compile cleanly for Docker `scratch` or cross-platform deployment without GCC/Clang dependencies.
3. **Idempotency & Immutability:** Inbound requests are recorded in the Event Ledger with a unique UUID (`evt-xxxxxxxx`) and duration metrics, enabling replayability and fail-safe auditing.
4. **Strict Concurrency Safety:** All packages pass `go test -race ./...` with zero data races.
