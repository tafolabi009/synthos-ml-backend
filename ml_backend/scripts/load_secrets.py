#!/usr/bin/env python3
"""
AWS Secrets Manager Integration for ML Backend
==============================================

Loads configuration from AWS Secrets Manager and sets environment variables.
Run this before starting the ML backend to auto-configure GPU settings.

Usage:
    # As entrypoint (in Dockerfile)
    CMD ["python", "scripts/load_secrets.py", "&&", "python", "server_production.py"]
    
    # Or import in server_production.py
    from scripts.load_secrets import load_secrets
    load_secrets()
"""

import os
import json
import logging
from typing import Dict, Optional

logger = logging.getLogger(__name__)


def load_secrets(
    secret_name: Optional[str] = None,
    region_name: Optional[str] = None,
    endpoint_url: Optional[str] = None
) -> Dict[str, str]:
    """
    Load secrets from AWS Secrets Manager and set as environment variables.
    
    Args:
        secret_name: Name of the secret in Secrets Manager
                    Default: ML_BACKEND_CONFIG or SYNTHOS_ML_CONFIG
        region_name: AWS region
                    Default: AWS_REGION or us-east-1
        endpoint_url: Custom endpoint (for local testing with LocalStack)
    
    Returns:
        Dictionary of loaded secrets
    """
    # Get secret name from env or use defaults
    secret_name = secret_name or os.getenv('SECRET_NAME') or os.getenv('ML_BACKEND_SECRET', 'synthos/ml-backend/config')
    region_name = region_name or os.getenv('AWS_REGION', 'us-east-1')
    
    logger.info(f"Loading secrets from: {secret_name} in {region_name}")
    
    try:
        import boto3
        from botocore.exceptions import ClientError
        
        # Create Secrets Manager client
        session = boto3.session.Session()
        client_kwargs = {
            'service_name': 'secretsmanager',
            'region_name': region_name
        }
        
        if endpoint_url:
            client_kwargs['endpoint_url'] = endpoint_url
            
        client = session.client(**client_kwargs)
        
        # Get secret value
        try:
            response = client.get_secret_value(SecretId=secret_name)
        except ClientError as e:
            error_code = e.response['Error']['Code']
            if error_code == 'ResourceNotFoundException':
                logger.warning(f"Secret '{secret_name}' not found, using environment defaults")
                return {}
            elif error_code == 'DecryptionFailureException':
                logger.error("Failed to decrypt secret. Check KMS permissions.")
                raise
            elif error_code == 'InvalidRequestException':
                logger.error("Invalid request to Secrets Manager")
                raise
            elif error_code == 'InvalidParameterException':
                logger.error("Invalid parameter in secret request")
                raise
            else:
                logger.error(f"Error retrieving secret: {e}")
                raise
        
        # Parse secret (JSON string)
        if 'SecretString' in response:
            secrets = json.loads(response['SecretString'])
        else:
            # Binary secret - decode and parse
            import base64
            secrets = json.loads(base64.b64decode(response['SecretBinary']).decode('utf-8'))
        
        # Set environment variables
        set_count = 0
        for key, value in secrets.items():
            # Convert to uppercase for env vars
            env_key = key.upper()
            
            # Only set if not already set (allow env override of secrets)
            if env_key not in os.environ:
                os.environ[env_key] = str(value)
                set_count += 1
                logger.debug(f"Set {env_key} from secrets")
            else:
                logger.debug(f"Skipped {env_key} (already set)")
        
        logger.info(f"Loaded {set_count} configuration values from Secrets Manager")
        
        # Log GPU configuration
        gpu_tier = os.getenv('GPU_TIER', 'auto')
        logger.info(f"GPU_TIER: {gpu_tier}")
        logger.info(f"MAX_GPU_MEMORY_FRACTION: {os.getenv('MAX_GPU_MEMORY_FRACTION', '0.9')}")
        logger.info(f"ENABLE_MIXED_PRECISION: {os.getenv('ENABLE_MIXED_PRECISION', 'true')}")
        
        return secrets
        
    except ImportError:
        logger.warning("boto3 not installed, skipping Secrets Manager integration")
        return {}
    except Exception as e:
        logger.error(f"Failed to load secrets: {e}")
        # Don't raise - allow service to start with env defaults
        return {}


def load_secrets_from_file(filepath: str) -> Dict[str, str]:
    """
    Load secrets from a local JSON file (for development/testing).
    
    Args:
        filepath: Path to JSON file with configuration
        
    Returns:
        Dictionary of loaded secrets
    """
    try:
        with open(filepath, 'r') as f:
            secrets = json.load(f)
        
        for key, value in secrets.items():
            env_key = key.upper()
            if env_key not in os.environ:
                os.environ[env_key] = str(value)
                
        logger.info(f"Loaded {len(secrets)} values from {filepath}")
        return secrets
        
    except FileNotFoundError:
        logger.debug(f"Config file not found: {filepath}")
        return {}
    except json.JSONDecodeError as e:
        logger.error(f"Invalid JSON in {filepath}: {e}")
        return {}


def setup_environment():
    """
    Main setup function - loads secrets from all available sources.
    
    Priority (highest to lowest):
    1. Environment variables (already set)
    2. AWS Secrets Manager
    3. Local config file (/app/config/secrets.json)
    4. Default values
    """
    # Try local config file first (for development)
    local_config_paths = [
        '/app/config/secrets.json',
        'config/secrets.json',
        os.path.expanduser('~/.synthos/config.json'),
    ]
    
    for path in local_config_paths:
        if os.path.exists(path):
            load_secrets_from_file(path)
            break
    
    # Then try AWS Secrets Manager (for production)
    if os.getenv('AWS_REGION') or os.getenv('AWS_DEFAULT_REGION'):
        load_secrets()
    
    # Log final configuration
    logger.info("=" * 60)
    logger.info("ML Backend Configuration")
    logger.info("=" * 60)
    logger.info(f"GPU_TIER: {os.getenv('GPU_TIER', 'auto')}")
    logger.info(f"MAX_GPU_MEMORY_FRACTION: {os.getenv('MAX_GPU_MEMORY_FRACTION', '0.9')}")
    logger.info(f"ENABLE_MIXED_PRECISION: {os.getenv('ENABLE_MIXED_PRECISION', 'true')}")
    logger.info(f"FORCE_SEQUENTIAL_TRAINING: {os.getenv('FORCE_SEQUENTIAL_TRAINING', 'false')}")
    logger.info(f"COLLAPSE_THRESHOLD: {os.getenv('COLLAPSE_THRESHOLD', '65.0')}")
    logger.info(f"DIVERSITY_THRESHOLD: {os.getenv('DIVERSITY_THRESHOLD', '50.0')}")
    logger.info("=" * 60)


# If run directly, just load secrets
if __name__ == "__main__":
    logging.basicConfig(
        level=logging.INFO,
        format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
    )
    setup_environment()
