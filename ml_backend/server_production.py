#!/usr/bin/env python3
"""
Production gRPC Server for ML Backend
Serves ValidationEngine, CollapseEngine, and DataService via gRPC
With PostgreSQL and Redis integration
"""

import asyncio
import logging
import signal
import sys
import os
from concurrent import futures
from pathlib import Path

import grpc
from grpc import aio

# Setup logging first
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s',
    handlers=[
        logging.StreamHandler(sys.stdout),
    ]
)
logger = logging.getLogger(__name__)

# Add src to path
sys.path.insert(0, str(Path(__file__).parent))
sys.path.insert(0, str(Path(__file__).parent / 'src'))

# Import generated protobuf stubs
from src.grpc_services import validation_pb2_grpc
from src.grpc_services.validation_server_complete import (
    ValidationEngineServicer,
    CollapseEngineServicer
)
from src.connections import init_connections, close_connections

# Configuration from environment
VALIDATION_PORT = int(os.getenv('VALIDATION_SERVICE_PORT', '50051'))
COLLAPSE_PORT = int(os.getenv('COLLAPSE_SERVICE_PORT', '50052'))
DATA_PORT = int(os.getenv('DATA_SERVICE_PORT', '50054'))
HOST = os.getenv('SERVICE_HOST', '0.0.0.0')
MAX_WORKERS = int(os.getenv('GRPC_MAX_WORKERS', '10'))
MAX_MESSAGE_SIZE = int(os.getenv('GRPC_MAX_MESSAGE_SIZE', '100000000'))  # 100MB

# Database configuration
DATABASE_URL = os.getenv('DATABASE_URL')
REDIS_URL = os.getenv('REDIS_URL')
REDIS_PASSWORD = os.getenv('REDIS_PASSWORD')

# GPU configuration
GPU_DEVICES = os.getenv('GPU_DEVICES', '0')
GPU_MEMORY_FRACTION = float(os.getenv('GPU_MEMORY_FRACTION', '0.8'))
ENABLE_MIXED_PRECISION = os.getenv('ENABLE_MIXED_PRECISION', 'true').lower() == 'true'

# Thresholds
COLLAPSE_THRESHOLD = float(os.getenv('COLLAPSE_THRESHOLD', '65.0'))
DIVERSITY_THRESHOLD = float(os.getenv('DIVERSITY_THRESHOLD', '50.0'))


class GracefulKiller:
    """Handle graceful shutdown on SIGTERM/SIGINT"""
    kill_now = False
    
    def __init__(self):
        signal.signal(signal.SIGINT, self.exit_gracefully)
        signal.signal(signal.SIGTERM, self.exit_gracefully)
    
    def exit_gracefully(self, *args):
        logger.info("Received shutdown signal")
        self.kill_now = True


async def serve():
    """Start all gRPC servers with database connections"""
    killer = GracefulKiller()
    
    # Initialize database connections
    logger.info("Initializing database connections...")
    try:
        conn_manager = await init_connections(
            database_url=DATABASE_URL,
            redis_url=REDIS_URL
        )
        logger.info("✅ Database connections initialized")
    except Exception as e:
        logger.warning(f"⚠️ Database connections not available: {e}")
        conn_manager = None
    
    # Service configuration
    config = {
        'database_url': DATABASE_URL,
        'redis_url': REDIS_URL,
        'gpu_devices': GPU_DEVICES,
        'collapse_threshold': COLLAPSE_THRESHOLD,
        'diversity_threshold': DIVERSITY_THRESHOLD,
        'connection_manager': conn_manager,
    }
    
    hardware_config = {
        'gpu_memory_fraction': GPU_MEMORY_FRACTION,
        'enable_mixed_precision': ENABLE_MIXED_PRECISION,
    }
    
    # Create async servers
    validation_server = aio.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_send_message_length', MAX_MESSAGE_SIZE),
            ('grpc.max_receive_message_length', MAX_MESSAGE_SIZE),
            ('grpc.keepalive_time_ms', 10000),
            ('grpc.keepalive_timeout_ms', 5000),
            ('grpc.keepalive_permit_without_calls', True),
            ('grpc.http2.max_pings_without_data', 0),
        ]
    )
    
    collapse_server = aio.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_send_message_length', MAX_MESSAGE_SIZE),
            ('grpc.max_receive_message_length', MAX_MESSAGE_SIZE),
            ('grpc.keepalive_time_ms', 10000),
            ('grpc.keepalive_timeout_ms', 5000),
            ('grpc.keepalive_permit_without_calls', True),
            ('grpc.http2.max_pings_without_data', 0),
        ]
    )
    
    # Initialize servicers
    logger.info("Initializing service handlers...")
    try:
        validation_servicer = ValidationEngineServicer(config, hardware_config)
        collapse_servicer = CollapseEngineServicer(config)
        
        # Register servicers
        validation_pb2_grpc.add_ValidationEngineServicer_to_server(
            validation_servicer, validation_server
        )
        validation_pb2_grpc.add_CollapseEngineServicer_to_server(
            collapse_servicer, collapse_server
        )
    except Exception as e:
        logger.error(f"Failed to initialize servicers: {e}")
        raise
    
    # Start servers
    validation_addr = f'{HOST}:{VALIDATION_PORT}'
    collapse_addr = f'{HOST}:{COLLAPSE_PORT}'
    
    validation_server.add_insecure_port(validation_addr)
    collapse_server.add_insecure_port(collapse_addr)
    
    await validation_server.start()
    await collapse_server.start()
    
    logger.info(f"✅ Validation Service listening on {validation_addr}")
    logger.info(f"✅ Collapse Service listening on {collapse_addr}")
    logger.info("🚀 All ML services started successfully")
    
    # Print configuration
    logger.info("=" * 50)
    logger.info("ML Backend Configuration:")
    logger.info(f"  - GPU Devices: {GPU_DEVICES}")
    logger.info(f"  - GPU Memory Fraction: {GPU_MEMORY_FRACTION}")
    logger.info(f"  - Mixed Precision: {ENABLE_MIXED_PRECISION}")
    logger.info(f"  - Collapse Threshold: {COLLAPSE_THRESHOLD}")
    logger.info(f"  - Diversity Threshold: {DIVERSITY_THRESHOLD}")
    logger.info(f"  - Max Workers: {MAX_WORKERS}")
    logger.info(f"  - Database: {'Connected' if DATABASE_URL else 'Not configured'}")
    logger.info(f"  - Redis: {'Connected' if REDIS_URL else 'Not configured'}")
    logger.info("=" * 50)
    
    # Health check endpoint (simple TCP check)
    async def health_check():
        """Periodic health check logging"""
        while not killer.kill_now:
            await asyncio.sleep(60)
            health = {"services": "healthy"}
            if conn_manager:
                health.update(await conn_manager.health_check())
            logger.debug(f"Health check: {health}")
    
    health_task = asyncio.create_task(health_check())
    
    # Wait for termination
    try:
        while not killer.kill_now:
            await asyncio.sleep(1)
    finally:
        logger.info("Shutting down gracefully...")
        
        # Cancel health check
        health_task.cancel()
        try:
            await health_task
        except asyncio.CancelledError:
            pass
        
        # Stop servers with grace period
        await validation_server.stop(5)
        await collapse_server.stop(5)
        
        # Close database connections
        await close_connections()
        
        logger.info("All services stopped")


def main():
    """Entry point for the ML Backend server"""
    # Check for GPU availability
    try:
        import torch
        if torch.cuda.is_available():
            gpu_count = torch.cuda.device_count()
            logger.info(f"🎮 GPU Support: {gpu_count} GPU(s) available")
            for i in range(gpu_count):
                logger.info(f"  GPU {i}: {torch.cuda.get_device_name(i)}")
        else:
            logger.warning("⚠️ No GPU detected, running on CPU")
    except ImportError:
        logger.warning("⚠️ PyTorch not installed, GPU detection skipped")
    
    # Run the server
    try:
        asyncio.run(serve())
    except KeyboardInterrupt:
        logger.info("Server interrupted by user")
        sys.exit(0)
    except Exception as e:
        logger.error(f"Server error: {e}", exc_info=True)
        sys.exit(1)


if __name__ == '__main__':
    main()
