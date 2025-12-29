"""
GPU Auto-Configuration System
=============================

Automatically detects available GPU hardware and optimizes training configuration.
Just set GPU_TIER in Secrets Manager and the system adapts automatically.

Supported configurations:
- p5.48xlarge: 8x H100 80GB (640GB total) - Maximum performance
- p4d.24xlarge: 8x A100 40GB (320GB total) - Production recommended  
- p4de.24xlarge: 8x A100 80GB (640GB total) - Large models
- p3.8xlarge: 4x V100 16GB (64GB total) - Budget production
- p3.16xlarge: 8x V100 16GB (128GB total) - Medium production
- g5.12xlarge: 4x A10G 24GB (96GB total) - Cost-effective
- g5.48xlarge: 8x A10G 24GB (192GB total) - Scaled inference
- g4dn.12xlarge: 4x T4 16GB (64GB total) - Development/testing

Environment Variables (set in AWS Secrets Manager):
- GPU_TIER: Override auto-detection (optional)
- MAX_GPU_MEMORY_FRACTION: 0.0-1.0, default 0.9
- ENABLE_MIXED_PRECISION: true/false
- FORCE_SEQUENTIAL_TRAINING: true/false for debugging
"""

import os
import logging
from dataclasses import dataclass, field
from typing import Dict, List, Optional, Tuple, Any
from enum import Enum

import torch

logger = logging.getLogger(__name__)


class GPUTier(Enum):
    """GPU performance tiers"""
    ULTRA = "ultra"      # H100 80GB
    HIGH = "high"        # A100 40GB/80GB
    MEDIUM = "medium"    # V100 16GB
    STANDARD = "standard"  # A10G 24GB
    BASIC = "basic"      # T4 16GB
    CPU = "cpu"          # No GPU


@dataclass
class GPUProfile:
    """Profile for a specific GPU model"""
    name: str
    vram_gb: int
    compute_tflops_fp16: float
    bandwidth_gbps: float
    tier: GPUTier
    supports_bf16: bool
    supports_nvlink: bool
    optimal_batch_multiplier: float  # Relative to baseline
    

@dataclass
class HardwareConfig:
    """Auto-generated hardware configuration"""
    # Detected hardware
    num_gpus: int
    gpu_model: str
    vram_per_gpu_gb: int
    total_vram_gb: int
    tier: GPUTier
    
    # Computed optimal settings
    cascade_config: Dict[str, Any]
    training_config: Dict[str, Any]
    memory_config: Dict[str, Any]
    
    # Performance estimates
    estimated_validation_hours: float
    quality_factor: float  # 0-1, 1.0 = optimal
    

# GPU database with performance profiles
GPU_PROFILES: Dict[str, GPUProfile] = {
    "H100": GPUProfile(
        name="NVIDIA H100 80GB HBM3",
        vram_gb=80,
        compute_tflops_fp16=1979,
        bandwidth_gbps=3350,
        tier=GPUTier.ULTRA,
        supports_bf16=True,
        supports_nvlink=True,
        optimal_batch_multiplier=4.0,
    ),
    "A100-SXM4-80GB": GPUProfile(
        name="NVIDIA A100 80GB SXM4",
        vram_gb=80,
        compute_tflops_fp16=312,
        bandwidth_gbps=2039,
        tier=GPUTier.HIGH,
        supports_bf16=True,
        supports_nvlink=True,
        optimal_batch_multiplier=2.0,
    ),
    "A100-SXM4-40GB": GPUProfile(
        name="NVIDIA A100 40GB SXM4",
        vram_gb=40,
        compute_tflops_fp16=312,
        bandwidth_gbps=1555,
        tier=GPUTier.HIGH,
        supports_bf16=True,
        supports_nvlink=True,
        optimal_batch_multiplier=1.5,
    ),
    "A100": GPUProfile(  # Generic A100
        name="NVIDIA A100",
        vram_gb=40,
        compute_tflops_fp16=312,
        bandwidth_gbps=1555,
        tier=GPUTier.HIGH,
        supports_bf16=True,
        supports_nvlink=True,
        optimal_batch_multiplier=1.5,
    ),
    "V100": GPUProfile(
        name="NVIDIA V100 16GB",
        vram_gb=16,
        compute_tflops_fp16=125,
        bandwidth_gbps=900,
        tier=GPUTier.MEDIUM,
        supports_bf16=False,
        supports_nvlink=True,
        optimal_batch_multiplier=0.5,
    ),
    "A10G": GPUProfile(
        name="NVIDIA A10G 24GB",
        vram_gb=24,
        compute_tflops_fp16=125,
        bandwidth_gbps=600,
        tier=GPUTier.STANDARD,
        supports_bf16=True,
        supports_nvlink=False,
        optimal_batch_multiplier=0.4,
    ),
    "T4": GPUProfile(
        name="NVIDIA T4 16GB",
        vram_gb=16,
        compute_tflops_fp16=65,
        bandwidth_gbps=320,
        tier=GPUTier.BASIC,
        supports_bf16=False,
        supports_nvlink=False,
        optimal_batch_multiplier=0.2,
    ),
    "L4": GPUProfile(
        name="NVIDIA L4 24GB",
        vram_gb=24,
        compute_tflops_fp16=121,
        bandwidth_gbps=300,
        tier=GPUTier.STANDARD,
        supports_bf16=True,
        supports_nvlink=False,
        optimal_batch_multiplier=0.35,
    ),
}


class GPUAutoConfig:
    """
    Automatic GPU detection and configuration.
    
    Usage:
        config = GPUAutoConfig()
        hardware = config.get_optimal_config()
        
        # Use in training
        batch_size = hardware.cascade_config['tier_1']['batch_size']
    """
    
    # Baseline batch sizes (for 4x H100 80GB)
    BASELINE_BATCH_SIZES = {
        'tier_1': 256,  # 76M params
        'tier_2': 128,  # 454M params
        'tier_3': 64,   # 983M params
    }
    
    # Model memory requirements (approximate, in GB)
    MODEL_MEMORY_GB = {
        'tier_1': 2,    # 76M params
        'tier_2': 8,    # 454M params
        'tier_3': 15,   # 983M params with gradients
    }
    
    def __init__(self):
        self.detected_gpus = self._detect_gpus()
        self.gpu_profile = self._get_gpu_profile()
        self.env_overrides = self._load_env_overrides()
        
    def _detect_gpus(self) -> Dict[str, Any]:
        """Detect available GPU hardware"""
        if not torch.cuda.is_available():
            logger.warning("No CUDA GPUs detected, falling back to CPU")
            return {
                'available': False,
                'count': 0,
                'names': [],
                'memory_gb': [],
            }
        
        num_gpus = torch.cuda.device_count()
        gpu_names = []
        gpu_memory = []
        
        for i in range(num_gpus):
            props = torch.cuda.get_device_properties(i)
            gpu_names.append(props.name)
            gpu_memory.append(props.total_memory / (1024**3))  # Convert to GB
            
        logger.info(f"Detected {num_gpus} GPUs: {gpu_names[0] if gpu_names else 'None'}")
        logger.info(f"Total VRAM: {sum(gpu_memory):.1f} GB")
        
        return {
            'available': True,
            'count': num_gpus,
            'names': gpu_names,
            'memory_gb': gpu_memory,
        }
    
    def _get_gpu_profile(self) -> Optional[GPUProfile]:
        """Match detected GPU to known profile"""
        if not self.detected_gpus['available']:
            return None
            
        gpu_name = self.detected_gpus['names'][0] if self.detected_gpus['names'] else ""
        
        # Match GPU name to profile
        for key, profile in GPU_PROFILES.items():
            if key.lower() in gpu_name.lower():
                logger.info(f"Matched GPU profile: {profile.name}")
                return profile
        
        # Fallback: create profile from detected specs
        logger.warning(f"Unknown GPU: {gpu_name}, using detected specs")
        vram = int(self.detected_gpus['memory_gb'][0]) if self.detected_gpus['memory_gb'] else 16
        
        return GPUProfile(
            name=gpu_name,
            vram_gb=vram,
            compute_tflops_fp16=100,  # Conservative estimate
            bandwidth_gbps=500,
            tier=GPUTier.STANDARD,
            supports_bf16=True,
            supports_nvlink=False,
            optimal_batch_multiplier=0.3,
        )
    
    def _load_env_overrides(self) -> Dict[str, Any]:
        """Load configuration overrides from environment/secrets"""
        return {
            'gpu_tier': os.getenv('GPU_TIER'),
            'max_memory_fraction': float(os.getenv('MAX_GPU_MEMORY_FRACTION', '0.9')),
            'enable_mixed_precision': os.getenv('ENABLE_MIXED_PRECISION', 'true').lower() == 'true',
            'force_sequential': os.getenv('FORCE_SEQUENTIAL_TRAINING', 'false').lower() == 'true',
            'max_batch_size': int(os.getenv('MAX_BATCH_SIZE', '0')) or None,
            'num_gpus_override': int(os.getenv('NUM_GPUS', '0')) or None,
        }
    
    def get_optimal_config(self) -> HardwareConfig:
        """Generate optimal configuration for detected hardware"""
        
        num_gpus = self.env_overrides['num_gpus_override'] or self.detected_gpus['count']
        
        if not self.detected_gpus['available'] or num_gpus == 0:
            return self._get_cpu_config()
        
        profile = self.gpu_profile
        vram_per_gpu = int(self.detected_gpus['memory_gb'][0]) if self.detected_gpus['memory_gb'] else profile.vram_gb
        total_vram = vram_per_gpu * num_gpus
        
        # Calculate optimal settings
        cascade_config = self._compute_cascade_config(num_gpus, vram_per_gpu, profile)
        training_config = self._compute_training_config(profile)
        memory_config = self._compute_memory_config(vram_per_gpu, profile)
        
        # Estimate performance
        estimated_hours = self._estimate_validation_time(num_gpus, profile)
        quality_factor = self._compute_quality_factor(cascade_config, profile)
        
        config = HardwareConfig(
            num_gpus=num_gpus,
            gpu_model=profile.name,
            vram_per_gpu_gb=vram_per_gpu,
            total_vram_gb=total_vram,
            tier=profile.tier,
            cascade_config=cascade_config,
            training_config=training_config,
            memory_config=memory_config,
            estimated_validation_hours=estimated_hours,
            quality_factor=quality_factor,
        )
        
        self._log_config(config)
        return config
    
    def _compute_cascade_config(
        self, 
        num_gpus: int, 
        vram_per_gpu: int, 
        profile: GPUProfile
    ) -> Dict[str, Any]:
        """Compute optimal cascade training configuration"""
        
        # Calculate how many models can fit per GPU for each tier
        usable_vram = vram_per_gpu * self.env_overrides['max_memory_fraction']
        
        # Tier 1: 76M params (~2GB each)
        tier1_models_per_gpu = max(1, int(usable_vram / self.MODEL_MEMORY_GB['tier_1']))
        tier1_parallel = min(10, tier1_models_per_gpu * num_gpus)
        tier1_batch = self._scale_batch_size('tier_1', profile)
        
        # Tier 2: 454M params (~8GB each)
        tier2_models_per_gpu = max(1, int(usable_vram / self.MODEL_MEMORY_GB['tier_2']))
        tier2_parallel = min(5, tier2_models_per_gpu * num_gpus)
        tier2_batch = self._scale_batch_size('tier_2', profile)
        
        # Tier 3: 983M params (~15GB each)
        tier3_fits = usable_vram >= self.MODEL_MEMORY_GB['tier_3']
        tier3_parallel = min(3, num_gpus) if tier3_fits else 1
        tier3_batch = self._scale_batch_size('tier_3', profile)
        
        # If Tier 3 doesn't fit, use gradient accumulation
        tier3_grad_accum = 1
        if not tier3_fits:
            # Reduce batch and accumulate gradients
            tier3_batch = max(8, tier3_batch // 4)
            tier3_grad_accum = 4
            logger.warning(f"Tier 3 models don't fit in {vram_per_gpu}GB, using gradient accumulation")
        
        return {
            'tier_1': {
                'num_models': 10,
                'params_millions': 76,
                'parallel_models': tier1_parallel,
                'batch_size': tier1_batch,
                'gpus': list(range(min(2, num_gpus))),
                'data_rows': 2_000_000,
                'gradient_accumulation': 1,
            },
            'tier_2': {
                'num_models': 5,
                'params_millions': 454,
                'parallel_models': tier2_parallel,
                'batch_size': tier2_batch,
                'gpus': list(range(min(3, num_gpus))),
                'data_rows': 10_000_000,
                'gradient_accumulation': 1,
            },
            'tier_3': {
                'num_models': 3,
                'params_millions': 983,
                'parallel_models': tier3_parallel,
                'batch_size': tier3_batch,
                'gpus': list(range(num_gpus)),
                'data_rows': 50_000_000,
                'gradient_accumulation': tier3_grad_accum,
            },
            'total_models': 18,
            'execution_mode': 'sequential' if self.env_overrides['force_sequential'] else 'adaptive',
        }
    
    def _scale_batch_size(self, tier: str, profile: GPUProfile) -> int:
        """Scale batch size based on GPU capabilities"""
        baseline = self.BASELINE_BATCH_SIZES[tier]
        scaled = int(baseline * profile.optimal_batch_multiplier)
        
        # Apply max batch size override if set
        if self.env_overrides['max_batch_size']:
            scaled = min(scaled, self.env_overrides['max_batch_size'])
        
        # Ensure minimum viable batch size for FFT efficiency
        min_batch = 8 if tier == 'tier_3' else 16
        return max(min_batch, scaled)
    
    def _compute_training_config(self, profile: GPUProfile) -> Dict[str, Any]:
        """Compute training hyperparameters for GPU"""
        
        # Precision selection
        if profile.tier == GPUTier.ULTRA:
            precision = 'bf16'
            enable_tf32 = True
        elif profile.supports_bf16:
            precision = 'bf16'
            enable_tf32 = True
        else:
            precision = 'fp16'
            enable_tf32 = False
        
        # Override from env
        if not self.env_overrides['enable_mixed_precision']:
            precision = 'fp32'
        
        return {
            'precision': precision,
            'enable_tf32': enable_tf32,
            'use_gradient_checkpointing': profile.tier in [GPUTier.STANDARD, GPUTier.BASIC, GPUTier.MEDIUM],
            'compile_models': profile.tier in [GPUTier.ULTRA, GPUTier.HIGH],
            'fft_backend': 'cufft' if profile.tier != GPUTier.CPU else 'numpy',
            'use_fft_optimization': True,
            'distributed_backend': 'nccl' if profile.supports_nvlink else 'gloo',
        }
    
    def _compute_memory_config(self, vram_per_gpu: int, profile: GPUProfile) -> Dict[str, Any]:
        """Compute memory management settings"""
        
        # More aggressive memory management for smaller GPUs
        aggressive = profile.tier in [GPUTier.STANDARD, GPUTier.BASIC, GPUTier.MEDIUM]
        
        return {
            'max_memory_fraction': self.env_overrides['max_memory_fraction'],
            'gradient_accumulation_base': 2 if aggressive else 1,
            'clear_cache_between_models': aggressive,
            'enable_cpu_offload': vram_per_gpu < 24,
            'pin_memory': not aggressive,  # Disable for memory-constrained
            'num_workers': 4 if vram_per_gpu >= 40 else 2,
        }
    
    def _estimate_validation_time(self, num_gpus: int, profile: GPUProfile) -> float:
        """Estimate total validation time in hours"""
        
        # Baseline: 4 hours with 4x H100
        baseline_hours = 4.0
        
        # Scale by compute power
        h100_tflops = 1979
        compute_ratio = h100_tflops / profile.compute_tflops_fp16
        
        # Scale by number of GPUs (assuming 4 baseline)
        gpu_ratio = 4 / num_gpus
        
        # Scale by bandwidth (affects data loading)
        h100_bandwidth = 3350
        bandwidth_penalty = min(2.0, h100_bandwidth / profile.bandwidth_gbps)
        
        estimated = baseline_hours * compute_ratio * gpu_ratio * (bandwidth_penalty * 0.3 + 0.7)
        
        return round(estimated, 1)
    
    def _compute_quality_factor(self, cascade_config: Dict, profile: GPUProfile) -> float:
        """Estimate quality impact (1.0 = optimal)"""
        
        # Batch size impact on FFT quality
        tier3_batch = cascade_config['tier_3']['batch_size']
        batch_factor = min(1.0, tier3_batch / self.BASELINE_BATCH_SIZES['tier_3'])
        
        # Parallelism impact
        tier3_parallel = cascade_config['tier_3']['parallel_models']
        parallel_factor = min(1.0, tier3_parallel / 3)
        
        # Gradient accumulation penalty
        grad_accum = cascade_config['tier_3']['gradient_accumulation']
        accum_factor = 1.0 if grad_accum == 1 else 0.95
        
        # Precision impact
        precision_factor = 1.0 if profile.supports_bf16 else 0.98
        
        quality = batch_factor * 0.4 + parallel_factor * 0.3 + accum_factor * 0.2 + precision_factor * 0.1
        return round(quality, 2)
    
    def _get_cpu_config(self) -> HardwareConfig:
        """Fallback configuration for CPU-only"""
        logger.warning("Running in CPU mode - this will be very slow!")
        
        return HardwareConfig(
            num_gpus=0,
            gpu_model="CPU",
            vram_per_gpu_gb=0,
            total_vram_gb=0,
            tier=GPUTier.CPU,
            cascade_config={
                'tier_1': {'num_models': 10, 'parallel_models': 1, 'batch_size': 32, 'gpus': [], 'data_rows': 100_000, 'gradient_accumulation': 4},
                'tier_2': {'num_models': 5, 'parallel_models': 1, 'batch_size': 16, 'gpus': [], 'data_rows': 500_000, 'gradient_accumulation': 8},
                'tier_3': {'num_models': 3, 'parallel_models': 1, 'batch_size': 8, 'gpus': [], 'data_rows': 1_000_000, 'gradient_accumulation': 16},
                'total_models': 18,
                'execution_mode': 'sequential',
            },
            training_config={
                'precision': 'fp32',
                'enable_tf32': False,
                'use_gradient_checkpointing': True,
                'compile_models': False,
                'fft_backend': 'numpy',
                'use_fft_optimization': False,
                'distributed_backend': 'gloo',
            },
            memory_config={
                'max_memory_fraction': 0.9,
                'gradient_accumulation_base': 8,
                'clear_cache_between_models': True,
                'enable_cpu_offload': False,
                'pin_memory': False,
                'num_workers': 1,
            },
            estimated_validation_hours=100.0,
            quality_factor=0.7,
        )
    
    def _log_config(self, config: HardwareConfig):
        """Log the generated configuration"""
        logger.info("=" * 60)
        logger.info("GPU AUTO-CONFIGURATION RESULTS")
        logger.info("=" * 60)
        logger.info(f"GPUs: {config.num_gpus}x {config.gpu_model}")
        logger.info(f"Total VRAM: {config.total_vram_gb} GB")
        logger.info(f"Performance Tier: {config.tier.value.upper()}")
        logger.info("-" * 60)
        logger.info("Cascade Configuration:")
        for tier in ['tier_1', 'tier_2', 'tier_3']:
            tc = config.cascade_config[tier]
            logger.info(f"  {tier}: {tc['num_models']} models, batch={tc['batch_size']}, parallel={tc['parallel_models']}")
        logger.info("-" * 60)
        logger.info(f"Training: precision={config.training_config['precision']}, backend={config.training_config['distributed_backend']}")
        logger.info(f"Estimated Time: {config.estimated_validation_hours} hours")
        logger.info(f"Quality Factor: {config.quality_factor * 100:.0f}%")
        logger.info("=" * 60)
    
    def to_dict(self) -> Dict[str, Any]:
        """Export configuration as dictionary (for JSON/YAML)"""
        config = self.get_optimal_config()
        return {
            'hardware': {
                'num_gpus': config.num_gpus,
                'gpu_model': config.gpu_model,
                'vram_per_gpu_gb': config.vram_per_gpu_gb,
                'total_vram_gb': config.total_vram_gb,
                'tier': config.tier.value,
            },
            'cascade_training': config.cascade_config,
            'training': config.training_config,
            'memory': config.memory_config,
            'estimates': {
                'validation_hours': config.estimated_validation_hours,
                'quality_factor': config.quality_factor,
            }
        }


# Convenience function for quick access
def get_hardware_config() -> HardwareConfig:
    """Get optimal hardware configuration for current environment"""
    return GPUAutoConfig().get_optimal_config()


def get_cascade_config() -> Dict[str, Any]:
    """Get cascade training configuration"""
    return GPUAutoConfig().get_optimal_config().cascade_config


# Example usage and testing
if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    
    config = GPUAutoConfig()
    hardware = config.get_optimal_config()
    
    print("\nConfiguration as dict:")
    import json
    print(json.dumps(config.to_dict(), indent=2))
