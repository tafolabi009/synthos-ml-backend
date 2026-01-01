#!/bin/bash
set -ex

# SynthOS ML Backend Bootstrap Script
# Runs on first boot of g5.12xlarge GPU instance

echo "=== SynthOS ML Backend Bootstrap ==="
echo "Instance: g5.12xlarge (4x A10G GPUs)"
echo "Started: $(date)"

# Update system
apt-get update
apt-get install -y docker.io docker-compose awscli jq

# Start Docker
systemctl enable docker
systemctl start docker

# Add ubuntu user to docker group
usermod -aG docker ubuntu

# Configure Docker for GPU
distribution=$(. /etc/os-release;echo $ID$VERSION_ID)
curl -s -L https://nvidia.github.io/nvidia-docker/gpgkey | apt-key add -
curl -s -L https://nvidia.github.io/nvidia-docker/$distribution/nvidia-docker.list | tee /etc/apt/sources.list.d/nvidia-docker.list
apt-get update
apt-get install -y nvidia-docker2
systemctl restart docker

# Create app directory
mkdir -p /opt/synthos/ml_backend
cd /opt/synthos/ml_backend

# Get configuration from Secrets Manager
export AWS_DEFAULT_REGION=us-east-1
SECRETS=$(aws secretsmanager get-secret-value --secret-id synthos/ml-backend/config --query SecretString --output text 2>/dev/null || echo '{}')

# Parse secrets or use defaults
export DATABASE_URL=$(echo $SECRETS | jq -r '.DATABASE_URL // empty' || echo "")
export REDIS_URL=$(echo $SECRETS | jq -r '.REDIS_URL // empty' || echo "")
export GPU_TIER=$(echo $SECRETS | jq -r '.GPU_TIER // "auto"')
export MAX_GPU_MEMORY_FRACTION=$(echo $SECRETS | jq -r '.MAX_GPU_MEMORY_FRACTION // "0.9"')
export ENABLE_MIXED_PRECISION=$(echo $SECRETS | jq -r '.ENABLE_MIXED_PRECISION // "true"')

# Login to ECR
aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin 570116615008.dkr.ecr.us-east-1.amazonaws.com

# Pull ML backend image
docker pull 570116615008.dkr.ecr.us-east-1.amazonaws.com/synthos-ml-backend:latest || echo "Image not found, will build"

# Create docker-compose for ML backend
cat > docker-compose.yml << 'EOF'
version: '3.8'

services:
  ml-backend:
    image: 570116615008.dkr.ecr.us-east-1.amazonaws.com/synthos-ml-backend:latest
    container_name: synthos-ml-backend
    restart: always
    runtime: nvidia
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: all
              capabilities: [gpu]
    ports:
      - "50051:50051"
      - "50052:50052"
      - "50054:50054"
    environment:
      - DATABASE_URL=${DATABASE_URL}
      - REDIS_URL=${REDIS_URL}
      - GPU_TIER=${GPU_TIER:-auto}
      - MAX_GPU_MEMORY_FRACTION=${MAX_GPU_MEMORY_FRACTION:-0.9}
      - ENABLE_MIXED_PRECISION=${ENABLE_MIXED_PRECISION:-true}
      - FORCE_SEQUENTIAL_TRAINING=${FORCE_SEQUENTIAL_TRAINING:-false}
      - COLLAPSE_THRESHOLD=${COLLAPSE_THRESHOLD:-65.0}
      - DIVERSITY_THRESHOLD=${DIVERSITY_THRESHOLD:-50.0}
      - AWS_DEFAULT_REGION=us-east-1
      - NVIDIA_VISIBLE_DEVICES=all
    volumes:
      - ml-data:/data
      - ml-models:/models
      - ml-cache:/cache
    healthcheck:
      test: ["CMD", "python", "-c", "import grpc; channel = grpc.insecure_channel('localhost:50051'); grpc.channel_ready_future(channel).result(timeout=5)"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 60s
    logging:
      driver: awslogs
      options:
        awslogs-group: /synthos/ml-backend
        awslogs-region: us-east-1
        awslogs-stream-prefix: ml

volumes:
  ml-data:
  ml-models:
  ml-cache:
EOF

# Create CloudWatch log group
aws logs create-log-group --log-group-name /synthos/ml-backend --region us-east-1 2>/dev/null || true

# Start the service
docker-compose up -d

# Install CloudWatch agent for metrics
wget https://s3.amazonaws.com/amazoncloudwatch-agent/ubuntu/amd64/latest/amazon-cloudwatch-agent.deb
dpkg -i -E ./amazon-cloudwatch-agent.deb

# Configure CloudWatch agent
cat > /opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json << 'EOF'
{
  "metrics": {
    "namespace": "SynthOS/MLBackend",
    "metrics_collected": {
      "nvidia_gpu": {
        "measurement": [
          "utilization_gpu",
          "utilization_memory",
          "memory_total",
          "memory_used",
          "memory_free",
          "temperature_gpu"
        ],
        "metrics_collection_interval": 60
      },
      "mem": {
        "measurement": ["mem_used_percent"]
      },
      "cpu": {
        "measurement": ["cpu_usage_active"]
      }
    }
  },
  "logs": {
    "logs_collected": {
      "files": {
        "collect_list": [
          {
            "file_path": "/var/log/syslog",
            "log_group_name": "/synthos/ml-backend/system",
            "log_stream_name": "{instance_id}"
          }
        ]
      }
    }
  }
}
EOF

# Start CloudWatch agent
/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -c file:/opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json -s

echo "=== Bootstrap Complete ==="
echo "Finished: $(date)"
echo "GPU Status:"
nvidia-smi

# Register with Cloud Map
INSTANCE_ID=$(curl -s http://169.254.169.254/latest/meta-data/instance-id)
PRIVATE_IP=$(curl -s http://169.254.169.254/latest/meta-data/local-ipv4)

# Register ML backend in Cloud Map
aws servicediscovery register-instance \
  --service-id srv-ml-backend \
  --instance-id $INSTANCE_ID \
  --attributes AWS_INSTANCE_IPV4=$PRIVATE_IP,AWS_INSTANCE_PORT=50051 \
  --region us-east-1 2>/dev/null || echo "Cloud Map registration skipped"

echo "ML Backend is running at $PRIVATE_IP:50051"
