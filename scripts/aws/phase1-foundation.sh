#!/bin/bash
# =============================================================================
# SynthOS AWS Infrastructure Setup - Phase 1: Foundation (Data Layer)
# =============================================================================
# This script creates the foundational AWS infrastructure:
# - VPC with public/private subnets
# - Security Groups
# - RDS PostgreSQL
# - ElastiCache Redis
# - S3 Buckets
# - Secrets Manager
# =============================================================================

set -e

# Configuration
AWS_REGION="${AWS_REGION:-us-east-1}"
PROJECT_NAME="${PROJECT_NAME:-synthos}"
ENVIRONMENT="${ENVIRONMENT:-production}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# =============================================================================
# Step 1: Create VPC
# =============================================================================
create_vpc() {
    log_info "Creating VPC..."
    
    VPC_ID=$(aws ec2 create-vpc \
        --cidr-block 10.0.0.0/16 \
        --region $AWS_REGION \
        --tag-specifications "ResourceType=vpc,Tags=[{Key=Name,Value=${PROJECT_NAME}-vpc},{Key=Environment,Value=${ENVIRONMENT}}]" \
        --query 'Vpc.VpcId' \
        --output text)
    
    # Enable DNS hostnames
    aws ec2 modify-vpc-attribute --vpc-id $VPC_ID --enable-dns-hostnames '{"Value":true}' --region $AWS_REGION
    aws ec2 modify-vpc-attribute --vpc-id $VPC_ID --enable-dns-support '{"Value":true}' --region $AWS_REGION
    
    log_info "VPC created: $VPC_ID"
    echo $VPC_ID
}

# =============================================================================
# Step 2: Create Subnets
# =============================================================================
create_subnets() {
    local VPC_ID=$1
    log_info "Creating subnets..."
    
    # Get availability zones
    AZS=$(aws ec2 describe-availability-zones --region $AWS_REGION --query 'AvailabilityZones[0:2].ZoneName' --output text)
    AZ1=$(echo $AZS | cut -d' ' -f1)
    AZ2=$(echo $AZS | cut -d' ' -f2)
    
    # Public Subnet 1
    PUBLIC_SUBNET_1=$(aws ec2 create-subnet \
        --vpc-id $VPC_ID \
        --cidr-block 10.0.1.0/24 \
        --availability-zone $AZ1 \
        --region $AWS_REGION \
        --tag-specifications "ResourceType=subnet,Tags=[{Key=Name,Value=${PROJECT_NAME}-public-1},{Key=Type,Value=public}]" \
        --query 'Subnet.SubnetId' \
        --output text)
    
    # Public Subnet 2
    PUBLIC_SUBNET_2=$(aws ec2 create-subnet \
        --vpc-id $VPC_ID \
        --cidr-block 10.0.2.0/24 \
        --availability-zone $AZ2 \
        --region $AWS_REGION \
        --tag-specifications "ResourceType=subnet,Tags=[{Key=Name,Value=${PROJECT_NAME}-public-2},{Key=Type,Value=public}]" \
        --query 'Subnet.SubnetId' \
        --output text)
    
    # Private Subnet 1 (for RDS, ElastiCache)
    PRIVATE_SUBNET_1=$(aws ec2 create-subnet \
        --vpc-id $VPC_ID \
        --cidr-block 10.0.10.0/24 \
        --availability-zone $AZ1 \
        --region $AWS_REGION \
        --tag-specifications "ResourceType=subnet,Tags=[{Key=Name,Value=${PROJECT_NAME}-private-1},{Key=Type,Value=private}]" \
        --query 'Subnet.SubnetId' \
        --output text)
    
    # Private Subnet 2 (for RDS, ElastiCache)
    PRIVATE_SUBNET_2=$(aws ec2 create-subnet \
        --vpc-id $VPC_ID \
        --cidr-block 10.0.11.0/24 \
        --availability-zone $AZ2 \
        --region $AWS_REGION \
        --tag-specifications "ResourceType=subnet,Tags=[{Key=Name,Value=${PROJECT_NAME}-private-2},{Key=Type,Value=private}]" \
        --query 'Subnet.SubnetId' \
        --output text)
    
    # Enable auto-assign public IP for public subnets
    aws ec2 modify-subnet-attribute --subnet-id $PUBLIC_SUBNET_1 --map-public-ip-on-launch --region $AWS_REGION
    aws ec2 modify-subnet-attribute --subnet-id $PUBLIC_SUBNET_2 --map-public-ip-on-launch --region $AWS_REGION
    
    log_info "Subnets created: $PUBLIC_SUBNET_1, $PUBLIC_SUBNET_2, $PRIVATE_SUBNET_1, $PRIVATE_SUBNET_2"
    echo "$PUBLIC_SUBNET_1 $PUBLIC_SUBNET_2 $PRIVATE_SUBNET_1 $PRIVATE_SUBNET_2"
}

# =============================================================================
# Step 3: Create Internet Gateway and Route Tables
# =============================================================================
create_internet_gateway() {
    local VPC_ID=$1
    log_info "Creating Internet Gateway..."
    
    IGW_ID=$(aws ec2 create-internet-gateway \
        --region $AWS_REGION \
        --tag-specifications "ResourceType=internet-gateway,Tags=[{Key=Name,Value=${PROJECT_NAME}-igw}]" \
        --query 'InternetGateway.InternetGatewayId' \
        --output text)
    
    aws ec2 attach-internet-gateway --internet-gateway-id $IGW_ID --vpc-id $VPC_ID --region $AWS_REGION
    
    log_info "Internet Gateway created and attached: $IGW_ID"
    echo $IGW_ID
}

create_route_tables() {
    local VPC_ID=$1
    local IGW_ID=$2
    local PUBLIC_SUBNETS="$3"
    
    log_info "Creating route tables..."
    
    # Create public route table
    PUBLIC_RT=$(aws ec2 create-route-table \
        --vpc-id $VPC_ID \
        --region $AWS_REGION \
        --tag-specifications "ResourceType=route-table,Tags=[{Key=Name,Value=${PROJECT_NAME}-public-rt}]" \
        --query 'RouteTable.RouteTableId' \
        --output text)
    
    # Add route to Internet Gateway
    aws ec2 create-route --route-table-id $PUBLIC_RT --destination-cidr-block 0.0.0.0/0 --gateway-id $IGW_ID --region $AWS_REGION
    
    # Associate public subnets
    for SUBNET in $PUBLIC_SUBNETS; do
        aws ec2 associate-route-table --route-table-id $PUBLIC_RT --subnet-id $SUBNET --region $AWS_REGION
    done
    
    log_info "Route tables configured"
}

# =============================================================================
# Step 4: Create Security Groups
# =============================================================================
create_security_groups() {
    local VPC_ID=$1
    log_info "Creating security groups..."
    
    # ALB Security Group (HTTP/HTTPS from internet)
    ALB_SG=$(aws ec2 create-security-group \
        --group-name "${PROJECT_NAME}-alb-sg" \
        --description "ALB Security Group - HTTP/HTTPS" \
        --vpc-id $VPC_ID \
        --region $AWS_REGION \
        --query 'GroupId' \
        --output text)
    
    aws ec2 authorize-security-group-ingress --group-id $ALB_SG --protocol tcp --port 80 --cidr 0.0.0.0/0 --region $AWS_REGION
    aws ec2 authorize-security-group-ingress --group-id $ALB_SG --protocol tcp --port 443 --cidr 0.0.0.0/0 --region $AWS_REGION
    
    # App Security Group (from ALB only)
    APP_SG=$(aws ec2 create-security-group \
        --group-name "${PROJECT_NAME}-app-sg" \
        --description "Application Security Group" \
        --vpc-id $VPC_ID \
        --region $AWS_REGION \
        --query 'GroupId' \
        --output text)
    
    aws ec2 authorize-security-group-ingress --group-id $APP_SG --protocol tcp --port 8000 --source-group $ALB_SG --region $AWS_REGION
    aws ec2 authorize-security-group-ingress --group-id $APP_SG --protocol tcp --port 8080 --source-group $ALB_SG --region $AWS_REGION
    
    # Internal Security Group (for internal services)
    INTERNAL_SG=$(aws ec2 create-security-group \
        --group-name "${PROJECT_NAME}-internal-sg" \
        --description "Internal Services Security Group" \
        --vpc-id $VPC_ID \
        --region $AWS_REGION \
        --query 'GroupId' \
        --output text)
    
    # Allow all traffic from App SG
    aws ec2 authorize-security-group-ingress --group-id $INTERNAL_SG --protocol -1 --source-group $APP_SG --region $AWS_REGION
    # Allow internal traffic within the group
    aws ec2 authorize-security-group-ingress --group-id $INTERNAL_SG --protocol -1 --source-group $INTERNAL_SG --region $AWS_REGION
    
    # Database Security Group
    DB_SG=$(aws ec2 create-security-group \
        --group-name "${PROJECT_NAME}-db-sg" \
        --description "Database Security Group" \
        --vpc-id $VPC_ID \
        --region $AWS_REGION \
        --query 'GroupId' \
        --output text)
    
    aws ec2 authorize-security-group-ingress --group-id $DB_SG --protocol tcp --port 5432 --source-group $APP_SG --region $AWS_REGION
    aws ec2 authorize-security-group-ingress --group-id $DB_SG --protocol tcp --port 5432 --source-group $INTERNAL_SG --region $AWS_REGION
    
    # Redis Security Group
    REDIS_SG=$(aws ec2 create-security-group \
        --group-name "${PROJECT_NAME}-redis-sg" \
        --description "Redis Security Group" \
        --vpc-id $VPC_ID \
        --region $AWS_REGION \
        --query 'GroupId' \
        --output text)
    
    aws ec2 authorize-security-group-ingress --group-id $REDIS_SG --protocol tcp --port 6379 --source-group $APP_SG --region $AWS_REGION
    aws ec2 authorize-security-group-ingress --group-id $REDIS_SG --protocol tcp --port 6379 --source-group $INTERNAL_SG --region $AWS_REGION
    
    log_info "Security groups created: ALB=$ALB_SG, APP=$APP_SG, INTERNAL=$INTERNAL_SG, DB=$DB_SG, REDIS=$REDIS_SG"
    echo "$ALB_SG $APP_SG $INTERNAL_SG $DB_SG $REDIS_SG"
}

# =============================================================================
# Step 5: Create RDS PostgreSQL
# =============================================================================
create_rds() {
    local PRIVATE_SUBNETS="$1"
    local DB_SG=$2
    log_info "Creating RDS PostgreSQL..."
    
    # Create DB subnet group
    aws rds create-db-subnet-group \
        --db-subnet-group-name "${PROJECT_NAME}-db-subnet" \
        --db-subnet-group-description "SynthOS DB Subnet Group" \
        --subnet-ids $PRIVATE_SUBNETS \
        --region $AWS_REGION
    
    # Generate random password
    DB_PASSWORD=$(openssl rand -base64 24 | tr -dc 'a-zA-Z0-9' | head -c 24)
    
    # Create RDS instance
    aws rds create-db-instance \
        --db-instance-identifier "${PROJECT_NAME}-db" \
        --db-instance-class db.t3.medium \
        --engine postgres \
        --engine-version 15.4 \
        --master-username synthos_admin \
        --master-user-password "$DB_PASSWORD" \
        --allocated-storage 100 \
        --storage-type gp3 \
        --storage-encrypted \
        --vpc-security-group-ids $DB_SG \
        --db-subnet-group-name "${PROJECT_NAME}-db-subnet" \
        --db-name synthosdb \
        --backup-retention-period 7 \
        --preferred-backup-window "03:00-04:00" \
        --preferred-maintenance-window "Mon:04:00-Mon:05:00" \
        --multi-az \
        --no-publicly-accessible \
        --auto-minor-version-upgrade \
        --deletion-protection \
        --copy-tags-to-snapshot \
        --tags Key=Name,Value="${PROJECT_NAME}-db" Key=Environment,Value=${ENVIRONMENT} \
        --region $AWS_REGION
    
    log_info "RDS PostgreSQL creation initiated. Password saved to Secrets Manager."
    echo "$DB_PASSWORD"
}

# =============================================================================
# Step 6: Create ElastiCache Redis
# =============================================================================
create_elasticache() {
    local PRIVATE_SUBNETS="$1"
    local REDIS_SG=$2
    log_info "Creating ElastiCache Redis..."
    
    # Create cache subnet group
    aws elasticache create-cache-subnet-group \
        --cache-subnet-group-name "${PROJECT_NAME}-redis-subnet" \
        --cache-subnet-group-description "SynthOS Redis Subnet Group" \
        --subnet-ids $PRIVATE_SUBNETS \
        --region $AWS_REGION
    
    # Generate auth token
    REDIS_AUTH_TOKEN=$(openssl rand -base64 32 | tr -dc 'a-zA-Z0-9' | head -c 32)
    
    # Create Redis cluster
    aws elasticache create-cache-cluster \
        --cache-cluster-id "${PROJECT_NAME}-redis" \
        --engine redis \
        --engine-version 7.0 \
        --cache-node-type cache.t3.medium \
        --num-cache-nodes 1 \
        --cache-subnet-group-name "${PROJECT_NAME}-redis-subnet" \
        --security-group-ids $REDIS_SG \
        --auth-token "$REDIS_AUTH_TOKEN" \
        --transit-encryption-enabled \
        --at-rest-encryption-enabled \
        --snapshot-retention-limit 7 \
        --preferred-maintenance-window "tue:05:00-tue:06:00" \
        --tags Key=Name,Value="${PROJECT_NAME}-redis" Key=Environment,Value=${ENVIRONMENT} \
        --region $AWS_REGION
    
    log_info "ElastiCache Redis creation initiated"
    echo "$REDIS_AUTH_TOKEN"
}

# =============================================================================
# Step 7: Create S3 Buckets
# =============================================================================
create_s3_buckets() {
    log_info "Creating S3 buckets..."
    
    # Datasets bucket
    aws s3 mb "s3://${PROJECT_NAME}-datasets-${AWS_REGION}" --region $AWS_REGION
    
    # Enable versioning
    aws s3api put-bucket-versioning \
        --bucket "${PROJECT_NAME}-datasets-${AWS_REGION}" \
        --versioning-configuration Status=Enabled \
        --region $AWS_REGION
    
    # Enable encryption
    aws s3api put-bucket-encryption \
        --bucket "${PROJECT_NAME}-datasets-${AWS_REGION}" \
        --server-side-encryption-configuration '{
            "Rules": [{
                "ApplyServerSideEncryptionByDefault": {
                    "SSEAlgorithm": "aws:kms"
                },
                "BucketKeyEnabled": true
            }]
        }' \
        --region $AWS_REGION
    
    # Block public access
    aws s3api put-public-access-block \
        --bucket "${PROJECT_NAME}-datasets-${AWS_REGION}" \
        --public-access-block-configuration '{
            "BlockPublicAcls": true,
            "IgnorePublicAcls": true,
            "BlockPublicPolicy": true,
            "RestrictPublicBuckets": true
        }' \
        --region $AWS_REGION
    
    # Models bucket
    aws s3 mb "s3://${PROJECT_NAME}-models-${AWS_REGION}" --region $AWS_REGION
    aws s3api put-bucket-versioning --bucket "${PROJECT_NAME}-models-${AWS_REGION}" --versioning-configuration Status=Enabled --region $AWS_REGION
    
    # Reports bucket
    aws s3 mb "s3://${PROJECT_NAME}-reports-${AWS_REGION}" --region $AWS_REGION
    
    log_info "S3 buckets created"
}

# =============================================================================
# Step 8: Create Secrets Manager Secret
# =============================================================================
create_secrets() {
    local DB_PASSWORD=$1
    local REDIS_AUTH_TOKEN=$2
    log_info "Creating Secrets Manager secret..."
    
    # Generate JWT secret
    JWT_SECRET=$(openssl rand -base64 48 | tr -dc 'a-zA-Z0-9' | head -c 64)
    
    # Create secret
    aws secretsmanager create-secret \
        --name "${PROJECT_NAME}/${ENVIRONMENT}/secrets" \
        --description "SynthOS production secrets" \
        --secret-string "{
            \"jwt_secret\": \"${JWT_SECRET}\",
            \"database_password\": \"${DB_PASSWORD}\",
            \"redis_password\": \"${REDIS_AUTH_TOKEN}\",
            \"database_url\": \"postgresql://synthos_admin:${DB_PASSWORD}@${PROJECT_NAME}-db.${AWS_REGION}.rds.amazonaws.com:5432/synthosdb\",
            \"redis_url\": \"rediss://${PROJECT_NAME}-redis.${AWS_REGION}.cache.amazonaws.com:6379\"
        }" \
        --tags Key=Name,Value="${PROJECT_NAME}-secrets" Key=Environment,Value=${ENVIRONMENT} \
        --region $AWS_REGION
    
    log_info "Secrets created in Secrets Manager"
}

# =============================================================================
# Main Execution
# =============================================================================
main() {
    log_info "Starting SynthOS AWS Infrastructure Setup - Phase 1"
    log_info "Region: $AWS_REGION, Project: $PROJECT_NAME, Environment: $ENVIRONMENT"
    
    # Step 1: Create VPC
    VPC_ID=$(create_vpc)
    
    # Step 2: Create Subnets
    SUBNETS=$(create_subnets $VPC_ID)
    PUBLIC_SUBNET_1=$(echo $SUBNETS | cut -d' ' -f1)
    PUBLIC_SUBNET_2=$(echo $SUBNETS | cut -d' ' -f2)
    PRIVATE_SUBNET_1=$(echo $SUBNETS | cut -d' ' -f3)
    PRIVATE_SUBNET_2=$(echo $SUBNETS | cut -d' ' -f4)
    
    # Step 3: Create Internet Gateway
    IGW_ID=$(create_internet_gateway $VPC_ID)
    create_route_tables $VPC_ID $IGW_ID "$PUBLIC_SUBNET_1 $PUBLIC_SUBNET_2"
    
    # Step 4: Create Security Groups
    SGS=$(create_security_groups $VPC_ID)
    ALB_SG=$(echo $SGS | cut -d' ' -f1)
    APP_SG=$(echo $SGS | cut -d' ' -f2)
    INTERNAL_SG=$(echo $SGS | cut -d' ' -f3)
    DB_SG=$(echo $SGS | cut -d' ' -f4)
    REDIS_SG=$(echo $SGS | cut -d' ' -f5)
    
    # Step 5: Create RDS
    DB_PASSWORD=$(create_rds "$PRIVATE_SUBNET_1 $PRIVATE_SUBNET_2" $DB_SG)
    
    # Step 6: Create ElastiCache
    REDIS_AUTH_TOKEN=$(create_elasticache "$PRIVATE_SUBNET_1 $PRIVATE_SUBNET_2" $REDIS_SG)
    
    # Step 7: Create S3 Buckets
    create_s3_buckets
    
    # Step 8: Create Secrets
    create_secrets "$DB_PASSWORD" "$REDIS_AUTH_TOKEN"
    
    # Output summary
    log_info "=" 
    log_info "Phase 1 Infrastructure Setup Complete!"
    log_info "="
    log_info "VPC ID: $VPC_ID"
    log_info "Public Subnets: $PUBLIC_SUBNET_1, $PUBLIC_SUBNET_2"
    log_info "Private Subnets: $PRIVATE_SUBNET_1, $PRIVATE_SUBNET_2"
    log_info "Security Groups: ALB=$ALB_SG, APP=$APP_SG, Internal=$INTERNAL_SG"
    log_info ""
    log_info "Save these values for Phase 2 (ECS/Fargate setup):"
    log_info "export VPC_ID=$VPC_ID"
    log_info "export PUBLIC_SUBNET_1=$PUBLIC_SUBNET_1"
    log_info "export PUBLIC_SUBNET_2=$PUBLIC_SUBNET_2"
    log_info "export PRIVATE_SUBNET_1=$PRIVATE_SUBNET_1"
    log_info "export PRIVATE_SUBNET_2=$PRIVATE_SUBNET_2"
    log_info "export ALB_SG=$ALB_SG"
    log_info "export APP_SG=$APP_SG"
    log_info "export INTERNAL_SG=$INTERNAL_SG"
    
    # Save to file
    cat > infrastructure-phase1-output.env << EOF
VPC_ID=$VPC_ID
PUBLIC_SUBNET_1=$PUBLIC_SUBNET_1
PUBLIC_SUBNET_2=$PUBLIC_SUBNET_2
PRIVATE_SUBNET_1=$PRIVATE_SUBNET_1
PRIVATE_SUBNET_2=$PRIVATE_SUBNET_2
ALB_SG=$ALB_SG
APP_SG=$APP_SG
INTERNAL_SG=$INTERNAL_SG
DB_SG=$DB_SG
REDIS_SG=$REDIS_SG
EOF
    
    log_info "Configuration saved to infrastructure-phase1-output.env"
}

# Run main
main "$@"
