# SynthOS ML Backend

Microservices architecture for the SynthOS validation platform. Detects model collapse in AI training data before training begins.

**Status:** Alpha — core implementation complete, not production ready.

## Architecture

```
├── ml_backend/              # Port 50051/50052 — Unified ML Service (Validation + Collapse)
├── validation_service/      # Port 50051 — Cascade Training & Diversity Analysis
├── collapse_service/        # Port 50053 — Collapse Detection & Recommendations
├── go_backend/              # Port 8000  — REST API Gateway (Go Fiber)
├── job_orchestrator/        # Port 8080  — Job Queue Management (REST + gRPC)
├── data_service/            # Port 50054 — Dataset Management
├── admin_dashboard/         # Admin UI (Go + HTML)
├── monitoring/              # Prometheus + Grafana configs
├── proto/                   # Protocol Buffer definitions
├── migrations/              # Database migrations
└── scripts/                 # Deployment & utility scripts
```

## Core Capabilities

- **Cascade Validation** — multi-scale proxy model training (1M–500M params) to detect data quality issues before full training
- **Collapse Detection** — spectral analysis and distribution metrics to identify early signs of model collapse
- **Differential Privacy** — OpenDP integration for mathematical privacy guarantees
- **Multi-format Ingestion** — CSV, JSON, Parquet, Excel with automatic schema detection
- **Quality Scoring** — real-time quality metrics with Great Expectations

## Stack

- **ML Services:** Python, PyTorch, gRPC
- **API Gateway:** Go (Fiber), REST
- **Storage:** PostgreSQL, Redis, S3/GCS
- **Infra:** Docker Compose, Prometheus, Grafana

## Quick Start

```bash
git clone https://github.com/tafolabi009/synthos-ml-backend.git
cd synthos-ml-backend

cp .env.example .env
# Edit .env with POSTGRES_PASSWORD, REDIS_PASSWORD, JWT_SECRET

docker-compose up -d
```

## Related

- [synthos-dev](https://github.com/tafolabi009/synthos-dev) — SynthOS core validation engine
- [synthos.dev](https://synthos.dev) — Product site
