#!/bin/bash
# =============================================================================
# SynthOS AWS Infrastructure Setup - Phase 2: GPU Node for ML Backend
# =============================================================================
# This script:
# - Launches EC2 GPU instance for ML workloads
# - Installs NVIDIA drivers and Docker
# - Deploys ML Backend container
# =============================================================================

set -e

# Configuration
AWS_REGION="${AWS_REGION:-us-east-1}"
PROJECT_NAME="${PROJECT_NAME:-synthos}"
ENVIRONMENT="${ENVIRONMENT:-production}"
KEY_NAME="${KEY_NAME:-synthos-key}"

# Load Phase 1 outputs
if [ -f "infrastructure-phase1-output.env" ]; then
    source infrastructure-phase1-output.env
fi

# GPU Instance Configuration
GPU_INSTANCE_TYPE="${GPU_INSTANCE_TYPE:-p3.2xlarge}"  # 1x V100 GPU
GPU_AMI="${GPU_AMI:-}"  # Will use Deep Learning AMI

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# =============================================================================
# Step 1: Find Deep Learning AMI
# =============================================================================
find_gpu_ami() {
    log_info "Finding Deep Learning AMI..."
    
    GPU_AMI=$(aws ec2 describe-images \
        --owners amazon \
        --filters "Name=name,Values=Deep Learning AMI GPU PyTorch*Ubuntu 22.04*" \
        --query 'sort_by(Images, &CreationDate)[-1].ImageId' \
        --output text \
        --region $AWS_REGION)
    
    if [ -z "$GPU_AMI" ] || [ "$GPU_AMI" == "None" ]; then
        # Fallback to standard Ubuntu with NVIDIA drivers
        GPU_AMI=$(aws ec2 describe-images \
            --owners amazon \
            --filters "Name=name,Values=*ubuntu-22.04*x86_64*" \
            --query 'sort_by(Images, &CreationDate)[-1].ImageId' \
            --output text \
            --region $AWS_REGION)
    fi
    
    log_info "Using AMI: $GPU_AMI"
    echo $GPU_AMI
}

# =============================================================================
# Step 2: Create GPU Security Group
# =============================================================================
create_gpu_security_group() {
    log_info "Creating GPU instance security group..."
    
    GPU_SG=$(aws ec2 create-security-group \
        --group-name "${PROJECT_NAME}-gpu-sg" \
        --description "GPU Instance Security Group" \
        --vpc-id $VPC_ID \
        --region $AWS_REGION \
        --query 'GroupId' \
        --output text)
    
    # SSH access (restrict to your IP in production)
    aws ec2 authorize-security-group-ingress --group-id $GPU_SG --protocol tcp --port 22 --cidr 0.0.0.0/0 --region $AWS_REGION
    
    # gRPC ports from internal services
    aws ec2 authorize-security-group-ingress --group-id $GPU_SG --protocol tcp --port 50051 --source-group $APP_SG --region $AWS_REGION
    aws ec2 authorize-security-group-ingress --group-id $GPU_SG --protocol tcp --port 50052 --source-group $APP_SG --region $AWS_REGION
    aws ec2 authorize-security-group-ingress --group-id $GPU_SG --protocol tcp --port 50054 --source-group $APP_SG --region $AWS_REGION
    
    # Allow from job orchestrator
    aws ec2 authorize-security-group-ingress --group-id $GPU_SG --protocol tcp --port 50051-50055 --source-group $INTERNAL_SG --region $AWS_REGION
    
    log_info "GPU Security Group created: $GPU_SG"
    echo $GPU_SG
}

# =============================================================================
# Step 3: Create IAM Role for GPU Instance
# =============================================================================
create_gpu_iam_role() {
    log_info "Creating IAM role for GPU instance..."
    
    # Create trust policy
    cat > /tmp/trust-policy.json << EOF
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Principal": {
                "Service": "ec2.amazonaws.com"
            },
            "Action": "sts:AssumeRole"
        }
    ]
}
EOF
    
    # Create role
    aws iam create-role \
        --role-name "${PROJECT_NAME}-gpu-role" \
        --assume-role-policy-document file:///tmp/trust-policy.json \
        --region $AWS_REGION 2>/dev/null || true
    
    # Attach policies
    aws iam attach-role-policy --role-name "${PROJECT_NAME}-gpu-role" --policy-arn arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess
    aws iam attach-role-policy --role-name "${PROJECT_NAME}-gpu-role" --policy-arn arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly
    aws iam attach-role-policy --role-name "${PROJECT_NAME}-gpu-role" --policy-arn arn:aws:iam::aws:policy/SecretsManagerReadWrite
    aws iam attach-role-policy --role-name "${PROJECT_NAME}-gpu-role" --policy-arn arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy
    
    # Create instance profile
    aws iam create-instance-profile --instance-profile-name "${PROJECT_NAME}-gpu-profile" 2>/dev/null || true
    aws iam add-role-to-instance-profile --instance-profile-name "${PROJECT_NAME}-gpu-profile" --role-name "${PROJECT_NAME}-gpu-role" 2>/dev/null || true
    
    # Wait for instance profile to be ready
    sleep 10
    
    log_info "IAM role and instance profile created"
}

# =============================================================================
# Step 4: Create User Data Script
# =============================================================================
create_user_data() {
    local ECR_REPO=$1
    
    cat << 'EOF'
#!/bin/bash
set -e

# Install Docker
apt-get update
apt-get install -y apt-transport-https ca-certificates curl software-properties-common
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | apt-key add -
add-apt-repository "deb [arch=amd64] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable"
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io

# Install NVIDIA Container Toolkit
distribution=$(. /etc/os-release;echo $ID$VERSION_ID)
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/$distribution/libnvidia-container.list | \
    sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
    tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
apt-get update
apt-get install -y nvidia-container-toolkit
nvidia-ctk runtime configure --runtime=docker
systemctl restart docker

# Install AWS CLI
curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
unzip awscliv2.zip
./aws/install

# Login to ECR
EOF

    echo "aws ecr get-login-password --region ${AWS_REGION} | docker login --username AWS --password-stdin ${ECR_REPO}"
    
    cat << 'EOF'

# Pull and run ML Backend
docker pull ${ML_BACKEND_IMAGE}

# Get secrets from Secrets Manager
SECRETS=$(aws secretsmanager get-secret-value --secret-id synthos/production/secrets --query SecretString --output text)
DATABASE_URL=$(echo $SECRETS | jq -r '.database_url')
REDIS_URL=$(echo $SECRETS | jq -r '.redis_url')
REDIS_PASSWORD=$(echo $SECRETS | jq -r '.redis_password')

# Run ML Backend container
docker run -d \
    --name ml_backend \
    --gpus all \
    --restart always \
    -p 50051:50051 \
    -p 50052:50052 \
    -p 50054:50054 \
    -e DATABASE_URL="$DATABASE_URL" \
    -e REDIS_URL="$REDIS_URL" \
    -e REDIS_PASSWORD="$REDIS_PASSWORD" \
    -e GPU_DEVICES="0" \
    -e GPU_MEMORY_FRACTION="0.9" \
    -e ENABLE_MIXED_PRECISION="true" \
    -v /data/models:/models \
    -v /data/cache:/cache \
    ${ML_BACKEND_IMAGE}

# Setup CloudWatch Logs agent
cat > /opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json << 'CWCONFIG'
{
    "logs": {
        "logs_collected": {
            "files": {
                "collect_list": [
                    {
                        "file_path": "/var/lib/docker/containers/*/*.log",
                        "log_group_name": "/synthos/ml-backend",
                        "log_stream_name": "{instance_id}",
                        "timestamp_format": "%Y-%m-%dT%H:%M:%S"
                    }
                ]
            }
        }
    }
}
CWCONFIG

systemctl start amazon-cloudwatch-agent
systemctl enable amazon-cloudwatch-agent

echo "ML Backend deployment complete!" | logger
EOF
}

# =============================================================================
# Step 5: Launch GPU Instance
# =============================================================================
launch_gpu_instance() {
    local GPU_AMI=$1
    local GPU_SG=$2
    local ECR_REPO=$3
    
    log_info "Launching GPU instance..."
    
    # Create user data
    USER_DATA=$(create_user_data $ECR_REPO | base64 -w 0)
    
    # Launch instance
    INSTANCE_ID=$(aws ec2 run-instances \
        --image-id $GPU_AMI \
        --instance-type $GPU_INSTANCE_TYPE \
        --key-name $KEY_NAME \
        --security-group-ids $GPU_SG \
        --subnet-id $PUBLIC_SUBNET_1 \
        --iam-instance-profile Name="${PROJECT_NAME}-gpu-profile" \
        --user-data "$USER_DATA" \
        --block-device-mappings '[{"DeviceName":"/dev/sda1","Ebs":{"VolumeSize":200,"VolumeType":"gp3","DeleteOnTermination":true}}]' \
        --tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=${PROJECT_NAME}-ml-backend},{Key=Environment,Value=${ENVIRONMENT}}]" \
        --region $AWS_REGION \
        --query 'Instances[0].InstanceId' \
        --output text)
    
    log_info "Instance launched: $INSTANCE_ID"
    
    # Wait for instance to be running
    log_info "Waiting for instance to be running..."
    aws ec2 wait instance-running --instance-ids $INSTANCE_ID --region $AWS_REGION
    
    # Get instance details
    INSTANCE_IP=$(aws ec2 describe-instances \
        --instance-ids $INSTANCE_ID \
        --query 'Reservations[0].Instances[0].PublicIpAddress' \
        --output text \
        --region $AWS_REGION)
    
    PRIVATE_IP=$(aws ec2 describe-instances \
        --instance-ids $INSTANCE_ID \
        --query 'Reservations[0].Instances[0].PrivateIpAddress' \
        --output text \
        --region $AWS_REGION)
    
    log_info "Instance running!"
    log_info "Public IP: $INSTANCE_IP"
    log_info "Private IP: $PRIVATE_IP"
    
    echo "$INSTANCE_ID $INSTANCE_IP $PRIVATE_IP"
}

# =============================================================================
# Main Execution
# =============================================================================
main() {
    log_info "Starting SynthOS AWS Infrastructure Setup - Phase 2 (GPU Node)"
    
    # Verify Phase 1 outputs
    if [ -z "$VPC_ID" ]; then
        log_error "VPC_ID not found. Please run Phase 1 first or set environment variables."
        exit 1
    fi
    
    # Get ECR repository
    ECR_REPO="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"
    
    # Step 1: Find GPU AMI
    if [ -z "$GPU_AMI" ]; then
        GPU_AMI=$(find_gpu_ami)
    fi
    
    # Step 2: Create GPU Security Group
    GPU_SG=$(create_gpu_security_group)
    
    # Step 3: Create IAM Role
    create_gpu_iam_role
    
    # Step 4 & 5: Launch GPU Instance
    INSTANCE_INFO=$(launch_gpu_instance $GPU_AMI $GPU_SG $ECR_REPO)
    INSTANCE_ID=$(echo $INSTANCE_INFO | cut -d' ' -f1)
    INSTANCE_IP=$(echo $INSTANCE_INFO | cut -d' ' -f2)
    PRIVATE_IP=$(echo $INSTANCE_INFO | cut -d' ' -f3)
    
    # Save output
    cat >> infrastructure-phase1-output.env << EOF
GPU_INSTANCE_ID=$INSTANCE_ID
GPU_PUBLIC_IP=$INSTANCE_IP
GPU_PRIVATE_IP=$PRIVATE_IP
GPU_SG=$GPU_SG
ML_BACKEND_ADDR=$PRIVATE_IP:50051
COLLAPSE_SERVICE_ADDR=$PRIVATE_IP:50052
DATA_SERVICE_ADDR=$PRIVATE_IP:50054
EOF
    
    log_info "="
    log_info "Phase 2 Complete! GPU Instance Deployed"
    log_info "="
    log_info "Instance ID: $INSTANCE_ID"
    log_info "Public IP: $INSTANCE_IP"
    log_info "Private IP: $PRIVATE_IP"
    log_info ""
    log_info "ML Backend will be available at:"
    log_info "  - Validation Service: $PRIVATE_IP:50051"
    log_info "  - Collapse Service: $PRIVATE_IP:50052"
    log_info "  - Data Service: $PRIVATE_IP:50054"
    log_info ""
    log_info "SSH Access: ssh -i ${KEY_NAME}.pem ubuntu@${INSTANCE_IP}"
    log_info ""
    log_info "Wait 5-10 minutes for Docker and ML services to start."
}

# Run main
main "$@"
