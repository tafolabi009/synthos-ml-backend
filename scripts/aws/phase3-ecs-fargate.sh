#!/bin/bash
# =============================================================================
# SynthOS AWS Infrastructure Setup - Phase 3: ECS/Fargate Orchestration
# =============================================================================
# This script:
# - Creates ECR repositories
# - Builds and pushes Docker images
# - Creates ECS Cluster
# - Creates Task Definitions
# - Creates ECS Services
# - Creates Application Load Balancer
# =============================================================================

set -e

# Configuration
AWS_REGION="${AWS_REGION:-us-east-1}"
PROJECT_NAME="${PROJECT_NAME:-synthos}"
ENVIRONMENT="${ENVIRONMENT:-production}"
AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)

# Load previous phase outputs
if [ -f "infrastructure-phase1-output.env" ]; then
    source infrastructure-phase1-output.env
fi

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

ECR_REPO="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"

# =============================================================================
# Step 1: Create ECR Repositories
# =============================================================================
create_ecr_repos() {
    log_info "Creating ECR repositories..."
    
    for repo in "go-backend" "job-orchestrator" "ml-backend" "data-service"; do
        aws ecr create-repository \
            --repository-name "${PROJECT_NAME}/${repo}" \
            --image-scanning-configuration scanOnPush=true \
            --encryption-configuration encryptionType=AES256 \
            --region $AWS_REGION 2>/dev/null || log_warn "Repository ${repo} already exists"
    done
    
    log_info "ECR repositories created"
}

# =============================================================================
# Step 2: Build and Push Docker Images
# =============================================================================
build_and_push_images() {
    log_info "Building and pushing Docker images..."
    
    # Login to ECR
    aws ecr get-login-password --region $AWS_REGION | docker login --username AWS --password-stdin $ECR_REPO
    
    # Build Go Backend
    log_info "Building Go Backend..."
    docker build -t ${ECR_REPO}/${PROJECT_NAME}/go-backend:latest \
        -f go_backend/Dockerfile.production \
        --build-arg VERSION=1.0.0 \
        --build-arg BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
        .
    docker push ${ECR_REPO}/${PROJECT_NAME}/go-backend:latest
    
    # Build Job Orchestrator
    log_info "Building Job Orchestrator..."
    docker build -t ${ECR_REPO}/${PROJECT_NAME}/job-orchestrator:latest \
        -f job_orchestrator/Dockerfile.production \
        --build-arg VERSION=1.0.0 \
        .
    docker push ${ECR_REPO}/${PROJECT_NAME}/job-orchestrator:latest
    
    # Build ML Backend
    log_info "Building ML Backend..."
    docker build -t ${ECR_REPO}/${PROJECT_NAME}/ml-backend:latest \
        -f ml_backend/Dockerfile.production \
        ml_backend/
    docker push ${ECR_REPO}/${PROJECT_NAME}/ml-backend:latest
    
    log_info "All images built and pushed"
}

# =============================================================================
# Step 3: Create ECS Cluster
# =============================================================================
create_ecs_cluster() {
    log_info "Creating ECS cluster..."
    
    aws ecs create-cluster \
        --cluster-name "${PROJECT_NAME}-cluster" \
        --capacity-providers FARGATE FARGATE_SPOT \
        --default-capacity-provider-strategy capacityProvider=FARGATE,weight=1 capacityProvider=FARGATE_SPOT,weight=3 \
        --settings name=containerInsights,value=enabled \
        --region $AWS_REGION
    
    log_info "ECS cluster created: ${PROJECT_NAME}-cluster"
}

# =============================================================================
# Step 4: Create ECS Task Execution Role
# =============================================================================
create_ecs_roles() {
    log_info "Creating ECS roles..."
    
    # Task Execution Role (for ECR pull and CloudWatch logs)
    cat > /tmp/ecs-task-execution-trust.json << EOF
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Principal": {
                "Service": "ecs-tasks.amazonaws.com"
            },
            "Action": "sts:AssumeRole"
        }
    ]
}
EOF
    
    aws iam create-role \
        --role-name "${PROJECT_NAME}-ecs-task-execution" \
        --assume-role-policy-document file:///tmp/ecs-task-execution-trust.json \
        --region $AWS_REGION 2>/dev/null || true
    
    aws iam attach-role-policy \
        --role-name "${PROJECT_NAME}-ecs-task-execution" \
        --policy-arn arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy
    
    # Custom policy for Secrets Manager
    cat > /tmp/secrets-policy.json << EOF
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": [
                "secretsmanager:GetSecretValue",
                "kms:Decrypt"
            ],
            "Resource": "*"
        }
    ]
}
EOF
    
    aws iam put-role-policy \
        --role-name "${PROJECT_NAME}-ecs-task-execution" \
        --policy-name SecretsAccess \
        --policy-document file:///tmp/secrets-policy.json
    
    # Task Role (for application access to AWS services)
    aws iam create-role \
        --role-name "${PROJECT_NAME}-ecs-task-role" \
        --assume-role-policy-document file:///tmp/ecs-task-execution-trust.json \
        --region $AWS_REGION 2>/dev/null || true
    
    aws iam attach-role-policy --role-name "${PROJECT_NAME}-ecs-task-role" --policy-arn arn:aws:iam::aws:policy/AmazonS3FullAccess
    aws iam attach-role-policy --role-name "${PROJECT_NAME}-ecs-task-role" --policy-arn arn:aws:iam::aws:policy/SecretsManagerReadWrite
    
    log_info "ECS roles created"
}

# =============================================================================
# Step 5: Create CloudWatch Log Groups
# =============================================================================
create_log_groups() {
    log_info "Creating CloudWatch log groups..."
    
    for service in "go-backend" "job-orchestrator" "ml-backend"; do
        aws logs create-log-group \
            --log-group-name "/${PROJECT_NAME}/${service}" \
            --region $AWS_REGION 2>/dev/null || true
        
        aws logs put-retention-policy \
            --log-group-name "/${PROJECT_NAME}/${service}" \
            --retention-in-days 30 \
            --region $AWS_REGION
    done
    
    log_info "Log groups created"
}

# =============================================================================
# Step 6: Create Task Definitions
# =============================================================================
create_task_definitions() {
    log_info "Creating task definitions..."
    
    EXECUTION_ROLE_ARN="arn:aws:iam::${AWS_ACCOUNT_ID}:role/${PROJECT_NAME}-ecs-task-execution"
    TASK_ROLE_ARN="arn:aws:iam::${AWS_ACCOUNT_ID}:role/${PROJECT_NAME}-ecs-task-role"
    
    # Go Backend Task Definition
    cat > /tmp/go-backend-task.json << EOF
{
    "family": "${PROJECT_NAME}-go-backend",
    "networkMode": "awsvpc",
    "requiresCompatibilities": ["FARGATE"],
    "cpu": "512",
    "memory": "1024",
    "executionRoleArn": "${EXECUTION_ROLE_ARN}",
    "taskRoleArn": "${TASK_ROLE_ARN}",
    "containerDefinitions": [
        {
            "name": "go-backend",
            "image": "${ECR_REPO}/${PROJECT_NAME}/go-backend:latest",
            "essential": true,
            "portMappings": [
                {
                    "containerPort": 8000,
                    "protocol": "tcp"
                }
            ],
            "environment": [
                {"name": "ENVIRONMENT", "value": "production"},
                {"name": "PORT", "value": "8000"},
                {"name": "AWS_REGION", "value": "${AWS_REGION}"},
                {"name": "S3_BUCKET", "value": "${PROJECT_NAME}-datasets-${AWS_REGION}"},
                {"name": "SECRETS_MANAGER_ID", "value": "${PROJECT_NAME}/${ENVIRONMENT}/secrets"},
                {"name": "ORCHESTRATOR_ADDR", "value": "http://job-orchestrator.${PROJECT_NAME}:8080"},
                {"name": "VALIDATION_SERVICE_ADDR", "value": "${GPU_PRIVATE_IP}:50051"},
                {"name": "COLLAPSE_SERVICE_ADDR", "value": "${GPU_PRIVATE_IP}:50052"},
                {"name": "ENABLE_METRICS", "value": "true"},
                {"name": "ENABLE_TRACING", "value": "true"},
                {"name": "ALLOWED_ORIGINS", "value": "https://app.synthos.ai,https://synthos.ai"}
            ],
            "secrets": [
                {
                    "name": "DATABASE_URL",
                    "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:${PROJECT_NAME}/${ENVIRONMENT}/secrets:database_url::"
                },
                {
                    "name": "JWT_SECRET",
                    "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:${PROJECT_NAME}/${ENVIRONMENT}/secrets:jwt_secret::"
                },
                {
                    "name": "REDIS_URL",
                    "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:${PROJECT_NAME}/${ENVIRONMENT}/secrets:redis_url::"
                }
            ],
            "logConfiguration": {
                "logDriver": "awslogs",
                "options": {
                    "awslogs-group": "/${PROJECT_NAME}/go-backend",
                    "awslogs-region": "${AWS_REGION}",
                    "awslogs-stream-prefix": "ecs"
                }
            },
            "healthCheck": {
                "command": ["CMD-SHELL", "curl -f http://localhost:8000/health/live || exit 1"],
                "interval": 30,
                "timeout": 5,
                "retries": 3,
                "startPeriod": 60
            }
        }
    ]
}
EOF
    
    aws ecs register-task-definition \
        --cli-input-json file:///tmp/go-backend-task.json \
        --region $AWS_REGION
    
    # Job Orchestrator Task Definition
    cat > /tmp/job-orchestrator-task.json << EOF
{
    "family": "${PROJECT_NAME}-job-orchestrator",
    "networkMode": "awsvpc",
    "requiresCompatibilities": ["FARGATE"],
    "cpu": "256",
    "memory": "512",
    "executionRoleArn": "${EXECUTION_ROLE_ARN}",
    "taskRoleArn": "${TASK_ROLE_ARN}",
    "containerDefinitions": [
        {
            "name": "job-orchestrator",
            "image": "${ECR_REPO}/${PROJECT_NAME}/job-orchestrator:latest",
            "essential": true,
            "portMappings": [
                {
                    "containerPort": 8080,
                    "protocol": "tcp"
                },
                {
                    "containerPort": 50053,
                    "protocol": "tcp"
                }
            ],
            "environment": [
                {"name": "ENVIRONMENT", "value": "production"},
                {"name": "HTTP_PORT", "value": "8080"},
                {"name": "GRPC_PORT", "value": "50053"},
                {"name": "WORKERS", "value": "10"},
                {"name": "VALIDATION_SERVICE_ADDR", "value": "${GPU_PRIVATE_IP}:50051"},
                {"name": "COLLAPSE_SERVICE_ADDR", "value": "${GPU_PRIVATE_IP}:50052"},
                {"name": "DATA_SERVICE_ADDR", "value": "${GPU_PRIVATE_IP}:50054"}
            ],
            "secrets": [
                {
                    "name": "DATABASE_URL",
                    "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:${PROJECT_NAME}/${ENVIRONMENT}/secrets:database_url::"
                },
                {
                    "name": "REDIS_PASSWORD",
                    "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:${PROJECT_NAME}/${ENVIRONMENT}/secrets:redis_password::"
                }
            ],
            "logConfiguration": {
                "logDriver": "awslogs",
                "options": {
                    "awslogs-group": "/${PROJECT_NAME}/job-orchestrator",
                    "awslogs-region": "${AWS_REGION}",
                    "awslogs-stream-prefix": "ecs"
                }
            },
            "healthCheck": {
                "command": ["CMD-SHELL", "curl -f http://localhost:8080/health || exit 1"],
                "interval": 30,
                "timeout": 5,
                "retries": 3,
                "startPeriod": 30
            }
        }
    ]
}
EOF
    
    aws ecs register-task-definition \
        --cli-input-json file:///tmp/job-orchestrator-task.json \
        --region $AWS_REGION
    
    log_info "Task definitions created"
}

# =============================================================================
# Step 7: Create Application Load Balancer
# =============================================================================
create_alb() {
    log_info "Creating Application Load Balancer..."
    
    # Create ALB
    ALB_ARN=$(aws elbv2 create-load-balancer \
        --name "${PROJECT_NAME}-alb" \
        --subnets $PUBLIC_SUBNET_1 $PUBLIC_SUBNET_2 \
        --security-groups $ALB_SG \
        --scheme internet-facing \
        --type application \
        --ip-address-type ipv4 \
        --region $AWS_REGION \
        --query 'LoadBalancers[0].LoadBalancerArn' \
        --output text)
    
    ALB_DNS=$(aws elbv2 describe-load-balancers \
        --load-balancer-arns $ALB_ARN \
        --query 'LoadBalancers[0].DNSName' \
        --output text \
        --region $AWS_REGION)
    
    # Create Target Group for Go Backend
    TG_ARN=$(aws elbv2 create-target-group \
        --name "${PROJECT_NAME}-go-backend-tg" \
        --protocol HTTP \
        --port 8000 \
        --vpc-id $VPC_ID \
        --target-type ip \
        --health-check-path "/health/live" \
        --health-check-interval-seconds 30 \
        --healthy-threshold-count 2 \
        --unhealthy-threshold-count 3 \
        --region $AWS_REGION \
        --query 'TargetGroups[0].TargetGroupArn' \
        --output text)
    
    # Create Listener
    aws elbv2 create-listener \
        --load-balancer-arn $ALB_ARN \
        --protocol HTTP \
        --port 80 \
        --default-actions Type=forward,TargetGroupArn=$TG_ARN \
        --region $AWS_REGION
    
    log_info "ALB created: $ALB_DNS"
    echo "$ALB_ARN $TG_ARN $ALB_DNS"
}

# =============================================================================
# Step 8: Create ECS Services
# =============================================================================
create_ecs_services() {
    local TG_ARN=$1
    log_info "Creating ECS services..."
    
    # Create Service Discovery Namespace
    aws servicediscovery create-private-dns-namespace \
        --name "${PROJECT_NAME}" \
        --vpc $VPC_ID \
        --region $AWS_REGION 2>/dev/null || true
    
    NAMESPACE_ID=$(aws servicediscovery list-namespaces \
        --query "Namespaces[?Name=='${PROJECT_NAME}'].Id" \
        --output text \
        --region $AWS_REGION)
    
    # Create Service Discovery for Job Orchestrator
    ORCH_SERVICE_ID=$(aws servicediscovery create-service \
        --name "job-orchestrator" \
        --namespace-id $NAMESPACE_ID \
        --dns-config "NamespaceId=${NAMESPACE_ID},DnsRecords=[{Type=A,TTL=60}]" \
        --region $AWS_REGION \
        --query 'Service.Id' \
        --output text)
    
    # Go Backend Service
    aws ecs create-service \
        --cluster "${PROJECT_NAME}-cluster" \
        --service-name "${PROJECT_NAME}-go-backend" \
        --task-definition "${PROJECT_NAME}-go-backend" \
        --desired-count 2 \
        --launch-type FARGATE \
        --platform-version LATEST \
        --network-configuration "awsvpcConfiguration={subnets=[$PUBLIC_SUBNET_1,$PUBLIC_SUBNET_2],securityGroups=[$APP_SG],assignPublicIp=ENABLED}" \
        --load-balancers "targetGroupArn=${TG_ARN},containerName=go-backend,containerPort=8000" \
        --health-check-grace-period-seconds 120 \
        --deployment-configuration "minimumHealthyPercent=50,maximumPercent=200" \
        --region $AWS_REGION
    
    # Job Orchestrator Service
    aws ecs create-service \
        --cluster "${PROJECT_NAME}-cluster" \
        --service-name "${PROJECT_NAME}-job-orchestrator" \
        --task-definition "${PROJECT_NAME}-job-orchestrator" \
        --desired-count 2 \
        --launch-type FARGATE \
        --platform-version LATEST \
        --network-configuration "awsvpcConfiguration={subnets=[$PUBLIC_SUBNET_1,$PUBLIC_SUBNET_2],securityGroups=[$INTERNAL_SG],assignPublicIp=ENABLED}" \
        --service-registries "registryArn=arn:aws:servicediscovery:${AWS_REGION}:${AWS_ACCOUNT_ID}:service/${ORCH_SERVICE_ID}" \
        --deployment-configuration "minimumHealthyPercent=50,maximumPercent=200" \
        --region $AWS_REGION
    
    log_info "ECS services created"
}

# =============================================================================
# Main Execution
# =============================================================================
main() {
    log_info "Starting SynthOS AWS Infrastructure Setup - Phase 3 (ECS/Fargate)"
    
    # Verify prerequisites
    if [ -z "$VPC_ID" ]; then
        log_error "VPC_ID not set. Run Phase 1 first."
        exit 1
    fi
    
    if [ -z "$GPU_PRIVATE_IP" ]; then
        log_warn "GPU_PRIVATE_IP not set. Run Phase 2 first or set ML_BACKEND_ADDR manually."
        GPU_PRIVATE_IP="localhost"
    fi
    
    # Execute steps
    create_ecr_repos
    create_ecs_cluster
    create_ecs_roles
    create_log_groups
    
    # Build and push images
    log_info "Do you want to build and push Docker images? (y/n)"
    read -r BUILD_IMAGES
    if [ "$BUILD_IMAGES" == "y" ]; then
        build_and_push_images
    fi
    
    create_task_definitions
    
    ALB_INFO=$(create_alb)
    ALB_ARN=$(echo $ALB_INFO | cut -d' ' -f1)
    TG_ARN=$(echo $ALB_INFO | cut -d' ' -f2)
    ALB_DNS=$(echo $ALB_INFO | cut -d' ' -f3)
    
    create_ecs_services $TG_ARN
    
    # Save outputs
    cat >> infrastructure-phase1-output.env << EOF
ALB_ARN=$ALB_ARN
TG_ARN=$TG_ARN
ALB_DNS=$ALB_DNS
ECR_REPO=$ECR_REPO
EOF
    
    log_info "="
    log_info "Phase 3 Complete! ECS/Fargate Deployed"
    log_info "="
    log_info "ALB DNS: $ALB_DNS"
    log_info ""
    log_info "API Endpoint: http://${ALB_DNS}/api/v1"
    log_info "Health Check: http://${ALB_DNS}/health"
    log_info ""
    log_info "Next Steps:"
    log_info "1. Configure Route 53 to point your domain to the ALB"
    log_info "2. Add HTTPS listener with ACM certificate"
    log_info "3. Update ALLOWED_ORIGINS with your domain"
}

# Run main
main "$@"
