#!/bin/bash
set -euo pipefail

# Synthos GCP Infrastructure Setup
# Run this once to set up all GCP infrastructure

PROJECT_ID="${GCP_PROJECT_ID:-cs-poc-eeli19j6nx85aphvzv6bmeb}"
REGION="${GCP_REGION:-us-central1}"

GREEN='\033[0;32m'
NC='\033[0m'
log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }

log_info "=== Synthos GCP Infrastructure Setup ==="
log_info "Project: ${PROJECT_ID}"
log_info "Region: ${REGION}"

# 1. Enable APIs
log_info "Enabling required APIs..."
gcloud services enable \
    run.googleapis.com \
    sqladmin.googleapis.com \
    redis.googleapis.com \
    storage.googleapis.com \
    artifactregistry.googleapis.com \
    secretmanager.googleapis.com \
    cloudbuild.googleapis.com \
    compute.googleapis.com \
    vpcaccess.googleapis.com \
    --project="${PROJECT_ID}"

# 2. Create VPC Connector for Cloud Run -> Cloud SQL/Redis
log_info "Creating VPC connector..."
gcloud compute networks vpc-access connectors create synthos-connector \
    --region="${REGION}" \
    --range="10.8.0.0/28" \
    --project="${PROJECT_ID}" 2>/dev/null || log_info "VPC connector already exists"

# 3. Create Cloud SQL instance
log_info "Creating Cloud SQL PostgreSQL instance..."
gcloud sql instances create synthos-db \
    --database-version=POSTGRES_15 \
    --tier=db-f1-micro \
    --region="${REGION}" \
    --storage-type=SSD \
    --storage-size=10GB \
    --availability-type=zonal \
    --project="${PROJECT_ID}" 2>/dev/null || log_info "Cloud SQL instance already exists"

# Set the database password
DB_PASSWORD=$(gcloud secrets versions access latest --secret=synthos-db-password --project="${PROJECT_ID}" 2>/dev/null || echo "synthos-db-$(openssl rand -hex 8)")
gcloud sql users set-password postgres \
    --instance=synthos-db \
    --password="${DB_PASSWORD}" \
    --project="${PROJECT_ID}" 2>/dev/null || true

# Create the synthos database
gcloud sql databases create synthos \
    --instance=synthos-db \
    --project="${PROJECT_ID}" 2>/dev/null || log_info "Database 'synthos' already exists"

# Create synthos user
gcloud sql users create synthos \
    --instance=synthos-db \
    --password="${DB_PASSWORD}" \
    --project="${PROJECT_ID}" 2>/dev/null || log_info "User 'synthos' already exists"

# 4. Create Memorystore Redis
log_info "Creating Memorystore Redis..."
gcloud redis instances create synthos-redis \
    --size=1 \
    --region="${REGION}" \
    --redis-version=redis_7_0 \
    --tier=basic \
    --project="${PROJECT_ID}" 2>/dev/null || log_info "Redis instance already exists"

# 5. Create GCS bucket
log_info "Creating GCS bucket..."
gcloud storage buckets create "gs://synthos-datasets-${PROJECT_ID}" \
    --location="${REGION}" \
    --uniform-bucket-level-access \
    --project="${PROJECT_ID}" 2>/dev/null || log_info "GCS bucket already exists"

# 6. Create Artifact Registry
log_info "Creating Artifact Registry..."
gcloud artifacts repositories create synthos \
    --repository-format=docker \
    --location="${REGION}" \
    --project="${PROJECT_ID}" 2>/dev/null || log_info "Artifact Registry already exists"

# 7. Create secrets
log_info "Creating secrets..."
echo -n "$(openssl rand -hex 32)" | gcloud secrets create synthos-jwt-secret \
    --data-file=- --replication-policy=automatic --project="${PROJECT_ID}" 2>/dev/null || log_info "JWT secret already exists"

echo -n "${DB_PASSWORD}" | gcloud secrets create synthos-db-password \
    --data-file=- --replication-policy=automatic --project="${PROJECT_ID}" 2>/dev/null || log_info "DB password secret already exists"

echo -n "$(openssl rand -hex 16)" | gcloud secrets create synthos-redis-password \
    --data-file=- --replication-policy=automatic --project="${PROJECT_ID}" 2>/dev/null || log_info "Redis password secret already exists"

# 8. Run database migrations
log_info "Running database migrations..."
CLOUD_SQL_CONN=$(gcloud sql instances describe synthos-db --format="value(connectionName)" --project="${PROJECT_ID}" 2>/dev/null || echo "")
if [ -n "${CLOUD_SQL_CONN}" ]; then
    log_info "Cloud SQL connection: ${CLOUD_SQL_CONN}"
    log_info "To run migrations, use Cloud SQL Proxy or connect via gcloud:"
    log_info "  gcloud sql connect synthos-db --user=synthos --project=${PROJECT_ID}"
    log_info "  Then run: \\i migrations/000001_initial_schema.up.sql"
    log_info "  Then run: \\i migrations/000002_add_credits_system.up.sql"
fi

# 9. Print summary
log_info ""
log_info "=== Infrastructure Setup Complete ==="
log_info ""
REDIS_HOST=$(gcloud redis instances describe synthos-redis --region="${REGION}" --format="value(host)" --project="${PROJECT_ID}" 2>/dev/null || echo "pending")
log_info "Cloud SQL Instance: synthos-db"
log_info "Cloud SQL Connection: ${CLOUD_SQL_CONN:-pending}"
log_info "Redis Host: ${REDIS_HOST}"
log_info "GCS Bucket: synthos-datasets-${PROJECT_ID}"
log_info "Artifact Registry: ${REGION}-docker.pkg.dev/${PROJECT_ID}/synthos"
log_info ""
log_info "Next steps:"
log_info "  1. Run migrations against Cloud SQL"
log_info "  2. Deploy services: ./scripts/gcp/deploy-cloud-run.sh all"
