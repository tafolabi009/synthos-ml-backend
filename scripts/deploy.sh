#!/bin/bash
# =============================================================================
# SynthOS Master Deployment Script
# =============================================================================
# One-command deployment for SynthOS to AWS production
# Usage: ./deploy.sh [phase|all]
# Phases: foundation, gpu, ecs, verify
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_header() { echo -e "\n${BLUE}========================================${NC}"; echo -e "${BLUE}  $1${NC}"; echo -e "${BLUE}========================================${NC}\n"; }

# Configuration
export AWS_REGION="${AWS_REGION:-us-east-1}"
export PROJECT_NAME="${PROJECT_NAME:-synthos}"
export ENVIRONMENT="${ENVIRONMENT:-production}"

# =============================================================================
# Pre-flight Checks
# =============================================================================
preflight_checks() {
    log_header "Pre-flight Checks"
    
    # Check AWS CLI
    if ! command -v aws &> /dev/null; then
        log_error "AWS CLI not found. Please install it first."
        exit 1
    fi
    log_info "AWS CLI found"
    
    # Check AWS credentials
    if ! aws sts get-caller-identity &> /dev/null; then
        log_error "AWS credentials not configured. Run 'aws configure' first."
        exit 1
    fi
    log_info "AWS credentials configured"
    
    # Get account ID
    export AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
    log_info "AWS Account: $AWS_ACCOUNT_ID"
    
    # Check Docker
    if ! command -v docker &> /dev/null; then
        log_error "Docker not found. Please install it first."
        exit 1
    fi
    log_info "Docker found"
    
    # Check Docker is running
    if ! docker info &> /dev/null; then
        log_error "Docker is not running. Please start Docker."
        exit 1
    fi
    log_info "Docker is running"
    
    log_info "All pre-flight checks passed!"
}

# =============================================================================
# Phase 1: Foundation
# =============================================================================
deploy_foundation() {
    log_header "Phase 1: Foundation (Data Layer)"
    
    if [ -f "${SCRIPT_DIR}/aws/phase1-foundation.sh" ]; then
        bash "${SCRIPT_DIR}/aws/phase1-foundation.sh"
    else
        log_error "phase1-foundation.sh not found"
        exit 1
    fi
    
    # Wait for RDS to be available
    log_info "Waiting for RDS instance to be available..."
    aws rds wait db-instance-available \
        --db-instance-identifier "${PROJECT_NAME}-db" \
        --region $AWS_REGION
    
    log_info "Phase 1 complete!"
}

# =============================================================================
# Phase 2: GPU Node
# =============================================================================
deploy_gpu() {
    log_header "Phase 2: GPU Node (ML Backend)"
    
    # Load Phase 1 outputs
    if [ -f "${SCRIPT_DIR}/infrastructure-phase1-output.env" ]; then
        source "${SCRIPT_DIR}/infrastructure-phase1-output.env"
    else
        log_error "Phase 1 not completed. Run 'deploy.sh foundation' first."
        exit 1
    fi
    
    if [ -f "${SCRIPT_DIR}/aws/phase2-gpu-node.sh" ]; then
        bash "${SCRIPT_DIR}/aws/phase2-gpu-node.sh"
    else
        log_error "phase2-gpu-node.sh not found"
        exit 1
    fi
    
    log_info "Phase 2 complete!"
}

# =============================================================================
# Phase 3: ECS/Fargate
# =============================================================================
deploy_ecs() {
    log_header "Phase 3: ECS/Fargate Orchestration"
    
    # Load previous outputs
    if [ -f "${SCRIPT_DIR}/infrastructure-phase1-output.env" ]; then
        source "${SCRIPT_DIR}/infrastructure-phase1-output.env"
    else
        log_error "Previous phases not completed."
        exit 1
    fi
    
    if [ -f "${SCRIPT_DIR}/aws/phase3-ecs-fargate.sh" ]; then
        bash "${SCRIPT_DIR}/aws/phase3-ecs-fargate.sh"
    else
        log_error "phase3-ecs-fargate.sh not found"
        exit 1
    fi
    
    log_info "Phase 3 complete!"
}

# =============================================================================
# Verify Deployment
# =============================================================================
verify_deployment() {
    log_header "Verifying Deployment"
    
    # Load outputs
    if [ -f "${SCRIPT_DIR}/infrastructure-phase1-output.env" ]; then
        source "${SCRIPT_DIR}/infrastructure-phase1-output.env"
    fi
    
    # Set API URL from ALB
    if [ -n "$ALB_DNS" ]; then
        export API_URL="http://${ALB_DNS}"
    fi
    
    # Set ML Backend host
    if [ -n "$GPU_PRIVATE_IP" ]; then
        export ML_BACKEND_HOST="${GPU_PRIVATE_IP}"
    fi
    
    # Run verification
    bash "${SCRIPT_DIR}/verify_production.sh"
}

# =============================================================================
# Build and Push Images Locally
# =============================================================================
build_images() {
    log_header "Building Docker Images"
    
    ECR_REPO="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"
    
    # Login to ECR
    aws ecr get-login-password --region $AWS_REGION | \
        docker login --username AWS --password-stdin $ECR_REPO
    
    # Build Go Backend
    log_info "Building Go Backend..."
    docker build -t ${ECR_REPO}/${PROJECT_NAME}/go-backend:latest \
        -f go_backend/Dockerfile.production \
        --build-arg VERSION=$(git rev-parse --short HEAD) \
        .
    docker push ${ECR_REPO}/${PROJECT_NAME}/go-backend:latest
    log_info "Go Backend pushed"
    
    # Build Job Orchestrator
    log_info "Building Job Orchestrator..."
    docker build -t ${ECR_REPO}/${PROJECT_NAME}/job-orchestrator:latest \
        -f job_orchestrator/Dockerfile.production \
        .
    docker push ${ECR_REPO}/${PROJECT_NAME}/job-orchestrator:latest
    log_info "Job Orchestrator pushed"
    
    # Build ML Backend
    log_info "Building ML Backend..."
    docker build -t ${ECR_REPO}/${PROJECT_NAME}/ml-backend:latest \
        -f ml_backend/Dockerfile.production \
        ml_backend/
    docker push ${ECR_REPO}/${PROJECT_NAME}/ml-backend:latest
    log_info "ML Backend pushed"
    
    log_info "All images built and pushed!"
}

# =============================================================================
# Full Deployment
# =============================================================================
deploy_all() {
    log_header "Full SynthOS Production Deployment"
    
    preflight_checks
    
    log_info "Starting full deployment..."
    log_info "This will take approximately 30-45 minutes."
    echo ""
    
    read -p "Continue with deployment? (y/n) " -n 1 -r
    echo ""
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        log_info "Deployment cancelled."
        exit 0
    fi
    
    deploy_foundation
    sleep 10
    
    deploy_gpu
    sleep 10
    
    build_images
    
    deploy_ecs
    sleep 30
    
    verify_deployment
    
    log_header "Deployment Complete!"
    
    # Show summary
    if [ -f "${SCRIPT_DIR}/infrastructure-phase1-output.env" ]; then
        source "${SCRIPT_DIR}/infrastructure-phase1-output.env"
    fi
    
    echo ""
    echo "=============================================="
    echo "  SynthOS Production Environment"
    echo "=============================================="
    echo ""
    echo "API Endpoint: http://${ALB_DNS}"
    echo "Health Check: http://${ALB_DNS}/health"
    echo ""
    echo "ML Backend (GPU):"
    echo "  - Instance: ${GPU_INSTANCE_ID}"
    echo "  - IP: ${GPU_PUBLIC_IP}"
    echo "  - Validation: ${GPU_PRIVATE_IP}:50051"
    echo "  - Collapse: ${GPU_PRIVATE_IP}:50052"
    echo ""
    echo "Next Steps:"
    echo "  1. Configure DNS to point to ALB"
    echo "  2. Add SSL certificate via ACM"
    echo "  3. Update ALLOWED_ORIGINS in ECS task"
    echo "  4. Set up monitoring dashboards"
    echo ""
    echo "=============================================="
}

# =============================================================================
# Cleanup
# =============================================================================
cleanup() {
    log_header "Cleanup AWS Resources"
    
    log_warn "This will delete ALL SynthOS AWS resources!"
    read -p "Are you sure? (type 'DELETE' to confirm) " -r
    
    if [ "$REPLY" != "DELETE" ]; then
        log_info "Cleanup cancelled."
        exit 0
    fi
    
    # Load outputs
    if [ -f "${SCRIPT_DIR}/infrastructure-phase1-output.env" ]; then
        source "${SCRIPT_DIR}/infrastructure-phase1-output.env"
    fi
    
    # Delete ECS services
    log_info "Deleting ECS services..."
    aws ecs update-service --cluster ${PROJECT_NAME}-cluster --service ${PROJECT_NAME}-go-backend --desired-count 0 --region $AWS_REGION || true
    aws ecs update-service --cluster ${PROJECT_NAME}-cluster --service ${PROJECT_NAME}-job-orchestrator --desired-count 0 --region $AWS_REGION || true
    aws ecs delete-service --cluster ${PROJECT_NAME}-cluster --service ${PROJECT_NAME}-go-backend --force --region $AWS_REGION || true
    aws ecs delete-service --cluster ${PROJECT_NAME}-cluster --service ${PROJECT_NAME}-job-orchestrator --force --region $AWS_REGION || true
    
    # Delete ECS cluster
    aws ecs delete-cluster --cluster ${PROJECT_NAME}-cluster --region $AWS_REGION || true
    
    # Terminate GPU instance
    if [ -n "$GPU_INSTANCE_ID" ]; then
        log_info "Terminating GPU instance..."
        aws ec2 terminate-instances --instance-ids $GPU_INSTANCE_ID --region $AWS_REGION || true
    fi
    
    # Delete ALB
    if [ -n "$ALB_ARN" ]; then
        log_info "Deleting ALB..."
        aws elbv2 delete-load-balancer --load-balancer-arn $ALB_ARN --region $AWS_REGION || true
    fi
    
    # Delete RDS
    log_info "Deleting RDS..."
    aws rds delete-db-instance --db-instance-identifier ${PROJECT_NAME}-db --skip-final-snapshot --delete-automated-backups --region $AWS_REGION || true
    
    # Delete ElastiCache
    log_info "Deleting ElastiCache..."
    aws elasticache delete-cache-cluster --cache-cluster-id ${PROJECT_NAME}-redis --region $AWS_REGION || true
    
    # Delete S3 buckets (must be empty first)
    log_info "Emptying and deleting S3 buckets..."
    aws s3 rm "s3://${PROJECT_NAME}-datasets-${AWS_REGION}" --recursive || true
    aws s3 rb "s3://${PROJECT_NAME}-datasets-${AWS_REGION}" || true
    
    log_info "Cleanup complete! VPC and security groups may need manual cleanup."
}

# =============================================================================
# Usage
# =============================================================================
usage() {
    echo "SynthOS Deployment Script"
    echo ""
    echo "Usage: $0 [command]"
    echo ""
    echo "Commands:"
    echo "  all         Full deployment (all phases)"
    echo "  foundation  Phase 1: VPC, RDS, Redis, S3"
    echo "  gpu         Phase 2: GPU EC2 instance for ML"
    echo "  ecs         Phase 3: ECS/Fargate services"
    echo "  build       Build and push Docker images"
    echo "  verify      Run verification tests"
    echo "  cleanup     Delete all resources (DANGEROUS)"
    echo "  help        Show this help"
    echo ""
}

# =============================================================================
# Main
# =============================================================================
case "${1:-help}" in
    all)
        deploy_all
        ;;
    foundation)
        preflight_checks
        deploy_foundation
        ;;
    gpu)
        preflight_checks
        deploy_gpu
        ;;
    ecs)
        preflight_checks
        deploy_ecs
        ;;
    build)
        preflight_checks
        build_images
        ;;
    verify)
        verify_deployment
        ;;
    cleanup)
        cleanup
        ;;
    help|*)
        usage
        ;;
esac
