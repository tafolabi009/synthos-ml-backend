"""
SynthOS Fused Spectral Entropy CUDA Kernel

High-performance CUDA kernel for spectral entropy computation with
automatic GPU architecture detection and optimization.

This package provides a fused CUDA kernel that replaces PyTorch's
multi-step spectral analysis pipeline (FFT → PSD → Normalize → Entropy)
with a single optimized kernel, achieving 2-5x speedup depending on
GPU architecture.

Supported GPU Architectures:
    - Pascal (GTX 10-series, sm_60): ~2x speedup
    - Volta (V100, sm_70): ~3x speedup  
    - Turing (RTX 20-series, sm_75): ~2.5x speedup
    - Ampere (A100/RTX 30, sm_80/86): ~3.5-4x speedup
    - Ada Lovelace (RTX 40-series, sm_89): ~4x speedup
    - Hopper (H100, sm_90): ~5x speedup

Basic Usage:
    >>> from synthos_kernel import SynthOSKernel, compute_spectral_entropy
    >>> import torch
    
    # Create kernel (auto-detects GPU)
    >>> kernel = SynthOSKernel()
    >>> print(f"Using: {kernel.active_arch}")
    
    # Compute spectral entropy
    >>> x = torch.randn(4096, 128, device='cuda', dtype=torch.float32)
    >>> entropy = kernel.compute_spectral_entropy(x)
    
    # Or use convenience function
    >>> entropy = compute_spectral_entropy(x)

Requirements:
    - CUDA-capable GPU with compute capability >= 6.0
    - CUDA Toolkit 11.0+
    - PyTorch 2.0+ with CUDA support
    - libsynthos.so (built from source)

Author: SynthOS ML Backend Team
Version: 1.0.0
License: See LICENSE file
"""

__version__ = "1.0.0"
__author__ = "SynthOS ML Backend Team"

# Import main classes and functions from python_wrapper
from .python_wrapper import (
    # Main class
    SynthOSKernel,
    
    # Convenience functions
    compute_spectral_entropy,
    get_kernel,
    benchmark_vs_pytorch,
    
    # Enums and types
    SynthosArch,
    SynthosError,
    SynthosDeviceInfo,
    SynthosWorkspaceInfo,
    
    # Utilities
    find_library,
)

__all__ = [
    # Main API
    "SynthOSKernel",
    "compute_spectral_entropy",
    "get_kernel",
    
    # Benchmarking
    "benchmark_vs_pytorch",
    
    # Types
    "SynthosArch",
    "SynthosError", 
    "SynthosDeviceInfo",
    "SynthosWorkspaceInfo",
    
    # Utilities
    "find_library",
    
    # Metadata
    "__version__",
    "__author__",
]
