# Synthos

AI training data validation platform that detects model collapse before it happens.

**Live at [synthos.dev](https://synthos.dev)** | A [Genovo Technologies](https://genovotech.com) product

## What It Does

Synthos validates AI training data across five quality dimensions -- distribution fidelity, feature correlation, temporal consistency, outlier detection, and schema compliance -- with 90%+ prediction accuracy. It helps AI teams avoid costly training failures by catching data quality issues before training begins.

## Architecture

```
Frontend (Next.js)  ->  API Gateway (Go/Fiber)  ->  Job Orchestrator (Go)
     |                       |                          |
  Vercel               Cloud Run               ML Backend (Python/PyTorch)
                         |                          |
                     Cloud SQL              Validation + Collapse Detection
                     Memorystore Redis      Data Service (GCS)
```

### Services

| Service | Language | Port | Purpose |
|---------|----------|------|---------|
| API Gateway | Go (Fiber) | 8080 | REST API, auth, routing |
| Job Orchestrator | Go | 8080 | Pipeline control, job scheduling |
| ML Backend | Python (PyTorch) | 50051-52 | Validation engine, collapse detection |
| Data Service | Go | 50055 | Dataset management, GCS storage |
| Frontend | TypeScript (Next.js) | 3000 | Customer dashboard, admin console |

### Role-Based Access

| Role | Access | Purpose |
|------|--------|---------|
| Admin | /admin | Full platform control, user management |
| Developer | /developer | API monitoring, service health, docs |
| Support | /support | Ticket management, user support |
| User | /dashboard | Upload data, run validations, billing |

## Quick Start

```bash
# Clone
git clone https://github.com/tafolabi009/ml_backend.git
cd ml_backend

# Configure
cp .env.example .env

# Run all services
docker-compose up -d

# API available at http://localhost:8080
# Frontend at http://localhost:3000
```

## Environment

Copy `.env.example` to `.env` and configure:

- `DATABASE_URL` -- PostgreSQL connection
- `REDIS_URL` / `REDIS_PASSWORD` -- Redis cache
- `JWT_SECRET` -- Auth token signing
- `GCP_PROJECT_ID` -- Google Cloud project
- `GCS_BUCKET` -- Dataset storage bucket

## Deployment

**Backend** -- GCP Cloud Run via Cloud Build:
```bash
gcloud builds submit --config cloudbuild-api.yaml --region us-central1
```

**Frontend** -- Auto-deploys to Vercel on push to `main` branch of the frontend repo.

## API Reference

Interactive API documentation available at the developer console after login.

Key endpoints:
- `POST /api/v1/auth/register` -- Create account
- `POST /api/v1/auth/login` -- Get JWT token
- `POST /api/v1/datasets/upload` -- Upload dataset
- `POST /api/v1/validations/create` -- Run validation
- `GET /api/v1/credits/balance` -- Check credits
- `POST /api/v1/tickets` -- Submit support ticket

## License

Proprietary. Copyright Genovo Technologies.
