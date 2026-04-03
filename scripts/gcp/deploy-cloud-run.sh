#!/bin/bash
set -euo pipefail

# Synthos GCP Cloud Run Deployment Script
# Usage: ./deploy-cloud-run.sh [service] (e.g., api-gateway, job-orchestrator, data-service, ml-backend)

PROJECT_ID="${GCP_PROJECT_ID:-cs-poc-eeli19j6nx85aphvzv6bmeb}"
REGION="${GCP_REGION:-us-central1}"
REGISTRY="${REGION}-docker.pkg.dev/${PROJECT_ID}/synthos"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# Get Cloud SQL connection name
get_cloud_sql_connection() {
    gcloud sql instances describe synthos-db --format="value(connectionName)" --project="${PROJECT_ID}" 2>/dev/null || echo ""
}

# Get Memorystore Redis host
get_redis_host() {
    gcloud redis instances describe synthos-redis --region="${REGION}" --format="value(host)" --project="${PROJECT_ID}" 2>/dev/null || echo ""
}

# Build and push Docker image
build_and_push() {
    local service=$1
    local dockerfile=$2
    local context=$3
    local tag="${REGISTRY}/${service}:latest"

    log_info "Building ${service}..."
    docker build -t "${tag}" -f "${dockerfile}" "${context}"

    log_info "Pushing ${service} to Artifact Registry..."
    docker push "${tag}"

    echo "${tag}"
}

# Deploy a Cloud Run service
deploy_service() {
    local service=$1
    local image=$2
    local port=$3
    local memory=$4
    local cpu=$5
    local env_vars=$6
    local extra_flags="${7:-}"

    log_info "Deploying ${service} to Cloud Run..."

    gcloud run deploy "${service}" \
        --image="${image}" \
        --platform=managed \
        --region="${REGION}" \
        --port="${port}" \
        --memory="${memory}" \
        --cpu="${cpu}" \
        --min-instances=0 \
        --max-instances=10 \
        --set-env-vars="${env_vars}" \
        --allow-unauthenticated \
        --project="${PROJECT_ID}" \
        ${extra_flags} \
        --quiet

    local url
    url=$(gcloud run services describe "${service}" --region="${REGION}" --format="value(status.url)" --project="${PROJECT_ID}")
    log_info "${service} deployed at: ${url}"
    echo "${url}"
}

# Get secrets references for Cloud Run
get_secret_ref() {
    local secret_name=$1
    echo "${secret_name}=projects/${PROJECT_ID}/secrets/${secret_name}/versions/latest"
}

# Main deployment
SERVICE="${1:-all}"
CLOUD_SQL_CONN=$(get_cloud_sql_connection)
REDIS_HOST=$(get_redis_host)
GCS_BUCKET="synthos-datasets-${PROJECT_ID}"

log_info "=== Synthos GCP Deployment ==="
log_info "Project: ${PROJECT_ID}"
log_info "Region: ${REGION}"
log_info "Cloud SQL: ${CLOUD_SQL_CONN}"
log_info "Redis: ${REDIS_HOST}"
log_info "GCS Bucket: ${GCS_BUCKET}"

# Configure Docker for Artifact Registry
gcloud auth configure-docker "${REGION}-docker.pkg.dev" --quiet 2>/dev/null

# Repo root
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

deploy_api_gateway() {
    local image
    image=$(build_and_push "api-gateway" "${REPO_ROOT}/go_backend/Dockerfile" "${REPO_ROOT}")

    deploy_service "synthos-api-gateway" "${image}" 8000 "512Mi" "1" \
        "ENVIRONMENT=production,CLOUD_PROVIDER=gcp,GCP_PROJECT_ID=${PROJECT_ID},GCP_REGION=${REGION},GCS_BUCKET=${GCS_BUCKET},REDIS_URL=${REDIS_HOST}:6379,ORCHESTRATOR_ADDR=https://synthos-job-orchestrator-${PROJECT_ID}.run.app,ENABLE_METRICS=true,ENABLE_TRACING=false,PORT=8000" \
        "--set-secrets=JWT_SECRET=synthos-jwt-secret:latest,REDIS_PASSWORD=synthos-redis-password:latest --add-cloudsql-instances=${CLOUD_SQL_CONN} --set-env-vars=DATABASE_URL=postgres://synthos:@/synthos?host=/cloudsql/${CLOUD_SQL_CONN}"
}

deploy_job_orchestrator() {
    local image
    image=$(build_and_push "job-orchestrator" "${REPO_ROOT}/job_orchestrator/Dockerfile" "${REPO_ROOT}")

    deploy_service "synthos-job-orchestrator" "${image}" 8080 "512Mi" "1" \
        "ENVIRONMENT=production,HTTP_PORT=8080,WORKERS=10,CLOUD_PROVIDER=gcp,VALIDATION_SERVICE_ADDR=synthos-ml-backend:50051,COLLAPSE_SERVICE_ADDR=synthos-ml-backend:50052,DATA_SERVICE_ADDR=synthos-ml-backend:50054,REDIS_URL=${REDIS_HOST}:6379,ENABLE_METRICS=true" \
        "--set-secrets=REDIS_PASSWORD=synthos-redis-password:latest --add-cloudsql-instances=${CLOUD_SQL_CONN} --set-env-vars=DATABASE_URL=postgres://synthos:@/synthos?host=/cloudsql/${CLOUD_SQL_CONN}"
}

deploy_data_service() {
    local image
    image=$(build_and_push "data-service" "${REPO_ROOT}/data_service/Dockerfile" "${REPO_ROOT}")

    deploy_service "synthos-data-service" "${image}" 50055 "512Mi" "1" \
        "ENVIRONMENT=production,PORT=50055,CLOUD_PROVIDER=gcp,GCS_BUCKET=${GCS_BUCKET},REDIS_URL=${REDIS_HOST}:6379,ENABLE_METRICS=true" \
        "--set-secrets=REDIS_PASSWORD=synthos-redis-password:latest --add-cloudsql-instances=${CLOUD_SQL_CONN} --set-env-vars=DATABASE_URL=postgres://synthos:@/synthos?host=/cloudsql/${CLOUD_SQL_CONN}"
}

deploy_ml_backend() {
    local image
    image=$(build_and_push "ml-backend" "${REPO_ROOT}/ml_backend/Dockerfile.production" "${REPO_ROOT}/ml_backend")

    # ML backend needs more resources (GPU ideally)
    deploy_service "synthos-ml-backend" "${image}" 50051 "4Gi" "4" \
        "ENVIRONMENT=production,CLOUD_PROVIDER=gcp,VALIDATION_SERVICE_PORT=50051,COLLAPSE_SERVICE_PORT=50052,DATA_SERVICE_PORT=50054,SERVICE_HOST=0.0.0.0,GCS_BUCKET=${GCS_BUCKET},REDIS_URL=${REDIS_HOST}:6379,LOG_LEVEL=INFO,LOG_FORMAT=json" \
        "--set-secrets=REDIS_PASSWORD=synthos-redis-password:latest --add-cloudsql-instances=${CLOUD_SQL_CONN} --set-env-vars=DATABASE_URL=postgres://synthos:@/synthos?host=/cloudsql/${CLOUD_SQL_CONN} --cpu-boost"
}

case "${SERVICE}" in
    api-gateway)
        deploy_api_gateway
        ;;
    job-orchestrator)
        deploy_job_orchestrator
        ;;
    data-service)
        deploy_data_service
        ;;
    ml-backend)
        deploy_ml_backend
        ;;
    all)
        log_info "Deploying all services..."
        deploy_ml_backend
        deploy_data_service
        deploy_job_orchestrator
        deploy_api_gateway
        log_info "=== All services deployed successfully ==="
        ;;
    *)
        log_error "Unknown service: ${SERVICE}"
        echo "Usage: $0 [api-gateway|job-orchestrator|data-service|ml-backend|all]"
        exit 1
        ;;
esac
