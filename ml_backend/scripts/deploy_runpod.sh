#!/bin/bash
# RunPod Deployment Script for SynthOS ML Backend
# This script helps deploy the ML backend to RunPod GPU cloud

set -e

echo "=== SynthOS ML Backend - RunPod Deployment ==="

# Check if runpodctl is installed
if ! command -v runpodctl &> /dev/null; then
    echo "Installing runpodctl..."
    wget -qO- cli.runpod.net | sudo bash
fi

# Configuration
RUNPOD_API_KEY="${RUNPOD_API_KEY:-}"
POD_NAME="synthos-ml-backend"
GPU_TYPE="NVIDIA A10G"
GPU_COUNT=4
CONTAINER_IMAGE="570116615008.dkr.ecr.us-east-1.amazonaws.com/synthos-ml-backend:latest"

# Check for API key
if [ -z "$RUNPOD_API_KEY" ]; then
    echo ""
    echo "ERROR: RUNPOD_API_KEY not set"
    echo ""
    echo "To get your API key:"
    echo "1. Go to https://www.runpod.io/console/user/settings"
    echo "2. Click 'API Keys' in the left sidebar"
    echo "3. Create a new API key"
    echo "4. Run: export RUNPOD_API_KEY='your-key-here'"
    echo ""
    exit 1
fi

# Configure runpodctl
runpodctl config --apiKey "$RUNPOD_API_KEY"

echo ""
echo "Available GPU types on RunPod:"
echo "  - NVIDIA A10G (24GB) - ~\$0.80/hr per GPU"
echo "  - NVIDIA A100 (40GB) - ~\$1.89/hr per GPU"
echo "  - NVIDIA A100 (80GB) - ~\$2.49/hr per GPU"
echo "  - NVIDIA RTX 4090 (24GB) - ~\$0.74/hr per GPU"
echo ""

# Create pod using runpodctl
echo "Creating RunPod with 4x A10G GPUs..."

# Note: RunPod uses a different deployment model
# We'll use their template system or direct API
echo ""
echo "=== Manual Steps in RunPod Console ==="
echo ""
echo "1. Go to: https://www.runpod.io/console/pods"
echo "2. Click 'Deploy' or '+ GPU Pod'"
echo "3. Select GPU: 4x NVIDIA A10G (~\$3.20/hr total)"
echo "4. Select Template: 'RunPod Pytorch 2.1' or custom"
echo "5. Set Container Image: pytorch/pytorch:2.1.0-cuda12.1-cudnn8-runtime"
echo "6. Set Volume: 100GB (for models and data)"
echo ""
echo "Or use the API deployment below..."
