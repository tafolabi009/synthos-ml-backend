"""
SynthOS Fused Spectral Entropy Kernel - Python Wrapper

High-performance CUDA kernel for spectral entropy computation with
automatic GPU architecture detection and optimization.

Features:
    - Zero-copy PyTorch tensor interop
    - Automatic GPU architecture detection and kernel dispatch
    - Multi-architecture support (Pascal through Hopper)
    - CUDA stream support for async execution
    - Comprehensive error handling with diagnostics

Usage:
    from synthos_kernel import SynthOSKernel
    
    kernel = SynthOSKernel()  # Auto-detect library and GPU
    entropy = kernel.compute_spectral_entropy(input_tensor)
    
    print(f"Using: {kernel.active_arch}")
    print(f"Expected speedup: {kernel.get_performance_info()['expected_speedup']}x")

Requirements:
    - PyTorch >= 2.0 with CUDA support
    - CUDA Toolkit >= 11.0
    - libsynthos.so (built from spectral.cu)

Author: SynthOS ML Backend Team
Version: 1.0.0
"""

import os
import sys
import ctypes
import logging
from pathlib import Path
from typing import Optional, Dict, Any, Tuple
from functools import lru_cache

import torch

# Configure logging
logger = logging.getLogger(__name__)

# ==============================================================================
# C Type Definitions (matching spectral.h)
# ==============================================================================

class SynthosError:
    """Error codes from spectral.h"""
    SUCCESS = 0
    INVALID_DEVICE = 1001
    UNSUPPORTED_ARCH = 1002
    INVALID_DIMENSIONS = 1003
    FFT_SIZE_NOT_POWER_OF_2 = 1004
    FFT_SIZE_TOO_LARGE = 1005
    NULL_POINTER = 1006
    CUFFT_FAILED = 1007
    NOT_INITIALIZED = 1008
    ALREADY_INITIALIZED = 1009
    WORKSPACE_TOO_SMALL = 1010
    CUDA_ERROR = 1011


class SynthosArch:
    """GPU architecture enumeration"""
    UNKNOWN = 0
    PASCAL = 60
    VOLTA = 70
    TURING = 75
    AMPERE = 80
    AMPERE_RTX = 86
    ADA = 89
    HOPPER = 90
    
    @classmethod
    def name(cls, arch: int) -> str:
        """Get human-readable architecture name"""
        names = {
            cls.PASCAL: "Pascal (GTX 10-series)",
            cls.VOLTA: "Volta (V100)",
            cls.TURING: "Turing (RTX 20-series)",
            cls.AMPERE: "Ampere (A100)",
            cls.AMPERE_RTX: "Ampere (RTX 30-series)",
            cls.ADA: "Ada Lovelace (RTX 40-series)",
            cls.HOPPER: "Hopper (H100)"
        }
        return names.get(arch, f"Unknown (sm_{arch})")


class SynthosDeviceInfo(ctypes.Structure):
    """Device information structure (matching C struct)"""
    _fields_ = [
        ("device_id", ctypes.c_int),
        ("compute_capability_major", ctypes.c_int),
        ("compute_capability_minor", ctypes.c_int),
        ("arch", ctypes.c_int),
        ("arch_name", ctypes.c_char_p),
        ("global_memory_bytes", ctypes.c_size_t),
        ("multiprocessor_count", ctypes.c_int),
        ("max_threads_per_block", ctypes.c_int),
        ("warp_size", ctypes.c_int),
        ("shared_memory_per_block", ctypes.c_size_t),
        ("max_grid_dim_x", ctypes.c_int),
        ("expected_speedup_vs_pytorch", ctypes.c_float),
    ]


class SynthosWorkspaceInfo(ctypes.Structure):
    """Workspace size requirements"""
    _fields_ = [
        ("fft_workspace_bytes", ctypes.c_size_t),
        ("reduction_workspace_bytes", ctypes.c_size_t),
        ("total_bytes", ctypes.c_size_t),
    ]


# ==============================================================================
# Library Loader
# ==============================================================================

def find_library(lib_path: Optional[str] = None) -> Path:
    """
    Find the SynthOS shared library.
    
    Search order:
        1. Explicit path (if provided)
        2. Same directory as this Python file
        3. build/ subdirectory
        4. Common system paths
        5. LD_LIBRARY_PATH
    
    Args:
        lib_path: Optional explicit path to libsynthos.so
    
    Returns:
        Path to the library
    
    Raises:
        FileNotFoundError: If library cannot be found
    """
    lib_name = "libsynthos.so"
    
    if lib_path and lib_path != "auto":
        path = Path(lib_path)
        if path.exists():
            return path
        raise FileNotFoundError(f"Specified library not found: {lib_path}")
    
    # Search locations
    search_paths = [
        Path(__file__).parent / lib_name,
        Path(__file__).parent / "build" / lib_name,
        Path(__file__).parent.parent / "build" / lib_name,
        Path.cwd() / lib_name,
        Path.cwd() / "build" / lib_name,
    ]
    
    # Add LD_LIBRARY_PATH locations
    ld_path = os.environ.get("LD_LIBRARY_PATH", "")
    for dir_path in ld_path.split(":"):
        if dir_path:
            search_paths.append(Path(dir_path) / lib_name)
    
    # System paths
    search_paths.extend([
        Path("/usr/local/lib") / lib_name,
        Path("/usr/lib") / lib_name,
    ])
    
    for path in search_paths:
        if path.exists():
            logger.debug(f"Found library at: {path}")
            return path
    
    # List tried paths in error message
    tried = "\n  ".join(str(p) for p in search_paths[:5])
    raise FileNotFoundError(
        f"Cannot find {lib_name}. Searched:\n  {tried}\n"
        f"Please build the library or specify the path explicitly."
    )


# ==============================================================================
# SynthOS Kernel Class
# ==============================================================================

class SynthOSKernel:
    """
    Zero-copy CUDA kernel wrapper with PyTorch interop and automatic GPU optimization.
    
    This class provides a Python interface to the fused spectral entropy CUDA kernel.
    It automatically detects the GPU architecture and selects the optimal kernel
    variant for best performance.
    
    Attributes:
        active_arch (str): Detected GPU architecture (e.g., "sm_80 Ampere A100")
        device_id (int): CUDA device ID
        
    Example:
        >>> kernel = SynthOSKernel()
        >>> print(f"Using: {kernel.active_arch}")
        Using: sm_80 Ampere (A100)
        
        >>> # Compute spectral entropy
        >>> input_tensor = torch.randn(4096, 128, device='cuda')
        >>> entropy = kernel.compute_spectral_entropy(input_tensor)
        >>> print(entropy.shape)
        torch.Size([128])
    """
    
    # Singleton instance for global usage
    _instance: Optional['SynthOSKernel'] = None
    _initialized: bool = False
    
    def __new__(cls, lib_path: str = "auto", device: int = 0):
        """Singleton pattern for efficiency - reuse initialization"""
        if cls._instance is None or not cls._initialized:
            cls._instance = super().__new__(cls)
        return cls._instance
    
    def __init__(self, lib_path: str = "auto", device: int = 0):
        """
        Initialize the SynthOS kernel.
        
        Args:
            lib_path: Path to libsynthos.so, or "auto" for auto-detection
            device: CUDA device ID (default: 0)
        
        Raises:
            RuntimeError: If GPU compute capability < 6.0 or initialization fails
            FileNotFoundError: If library cannot be found
        """
        if SynthOSKernel._initialized and hasattr(self, '_lib'):
            return  # Already initialized
        
        # Verify CUDA availability
        if not torch.cuda.is_available():
            raise RuntimeError(
                "CUDA is not available. SynthOS kernel requires a CUDA-capable GPU."
            )
        
        # Find and load library
        lib_file = find_library(lib_path)
        logger.info(f"Loading SynthOS library from: {lib_file}")
        
        try:
            self._lib = ctypes.CDLL(str(lib_file))
        except OSError as e:
            raise RuntimeError(f"Failed to load library: {e}")
        
        # Setup function signatures
        self._setup_function_signatures()
        
        # Initialize the library
        self._device_id = device
        result = self._lib.synthos_init(device)
        
        if result != 0:
            error_msg = self._lib.synthos_get_error_string(result)
            raise RuntimeError(
                f"SynthOS initialization failed: {error_msg.decode('utf-8')} "
                f"(error code: {result})"
            )
        
        # Get active architecture
        arch_ptr = self._lib.synthos_get_active_arch()
        self.active_arch = arch_ptr.decode('utf-8') if arch_ptr else "Unknown"
        
        # Cache device info
        self._device_info = SynthosDeviceInfo()
        self._lib.synthos_get_device_info(ctypes.byref(self._device_info))
        
        SynthOSKernel._initialized = True
        
        logger.info(
            f"SynthOS Kernel initialized: {self.active_arch} "
            f"(expected speedup: {self._device_info.expected_speedup_vs_pytorch:.1f}x)"
        )
        
        # Warn if using older architecture
        if self._device_info.arch < SynthosArch.VOLTA:
            logger.warning(
                f"GPU {self.active_arch} is older than recommended. "
                f"Consider upgrading to Volta (V100) or newer for best performance."
            )
    
    def _setup_function_signatures(self):
        """Configure ctypes function signatures for type safety"""
        
        # synthos_init
        self._lib.synthos_init.argtypes = [ctypes.c_int]
        self._lib.synthos_init.restype = ctypes.c_int
        
        # synthos_fused_spectral_entropy
        self._lib.synthos_fused_spectral_entropy.argtypes = [
            ctypes.c_void_p,  # d_input
            ctypes.c_void_p,  # d_entropy
            ctypes.c_int,     # n_samples
            ctypes.c_int,     # n_channels
            ctypes.c_void_p,  # stream
        ]
        self._lib.synthos_fused_spectral_entropy.restype = ctypes.c_int
        
        # synthos_get_active_arch
        self._lib.synthos_get_active_arch.argtypes = []
        self._lib.synthos_get_active_arch.restype = ctypes.c_char_p
        
        # synthos_get_device_info
        self._lib.synthos_get_device_info.argtypes = [ctypes.POINTER(SynthosDeviceInfo)]
        self._lib.synthos_get_device_info.restype = ctypes.c_int
        
        # synthos_get_workspace_size
        self._lib.synthos_get_workspace_size.argtypes = [
            ctypes.c_int,
            ctypes.c_int,
            ctypes.POINTER(SynthosWorkspaceInfo),
        ]
        self._lib.synthos_get_workspace_size.restype = ctypes.c_int
        
        # synthos_get_error_string
        self._lib.synthos_get_error_string.argtypes = [ctypes.c_int]
        self._lib.synthos_get_error_string.restype = ctypes.c_char_p
        
        # synthos_is_device_supported
        self._lib.synthos_is_device_supported.argtypes = [ctypes.c_int]
        self._lib.synthos_is_device_supported.restype = ctypes.c_int
        
        # synthos_cleanup
        self._lib.synthos_cleanup.argtypes = []
        self._lib.synthos_cleanup.restype = ctypes.c_int
        
        # synthos_synchronize
        self._lib.synthos_synchronize.argtypes = []
        self._lib.synthos_synchronize.restype = ctypes.c_int
        
        # synthos_get_version
        self._lib.synthos_get_version.argtypes = []
        self._lib.synthos_get_version.restype = ctypes.c_char_p
    
    def compute_spectral_entropy(
        self,
        input_tensor: torch.Tensor,
        stream: Optional[torch.cuda.Stream] = None
    ) -> torch.Tensor:
        """
        Compute per-channel spectral entropy using the fused CUDA kernel.
        
        This method performs the following operations in a single fused kernel:
            1. Batch real-to-complex FFT
            2. Power spectral density computation
            3. L1 normalization per channel
            4. Spectral entropy: -sum(p * log(p))
        
        Args:
            input_tensor: Input signal tensor of shape [N, M] where:
                - N: Number of samples (FFT size, must be power of 2, max 8192)
                - M: Number of channels (batch dimension)
                - dtype: torch.float32
                - device: 'cuda'
            stream: Optional CUDA stream for async execution
        
        Returns:
            Per-channel spectral entropy tensor of shape [M], dtype=float32
        
        Raises:
            RuntimeError: On CUDA errors or invalid inputs
            ValueError: If tensor properties are invalid
        
        Example:
            >>> kernel = SynthOSKernel()
            >>> x = torch.randn(4096, 128, device='cuda', dtype=torch.float32)
            >>> entropy = kernel.compute_spectral_entropy(x)
            >>> print(f"Entropy shape: {entropy.shape}, mean: {entropy.mean():.4f}")
        """
        # Validate input tensor
        self._validate_tensor(input_tensor)
        
        n_samples, n_channels = input_tensor.shape
        
        # Check FFT size constraints
        if n_samples > 8192:
            raise ValueError(
                f"FFT size {n_samples} exceeds maximum of 8192. "
                f"Consider downsampling or chunking the input."
            )
        
        if not (n_samples & (n_samples - 1) == 0):
            raise ValueError(
                f"FFT size {n_samples} must be a power of 2. "
                f"Next valid size: {self._next_power_of_2(n_samples)}"
            )
        
        # Ensure contiguous memory layout
        if not input_tensor.is_contiguous():
            input_tensor = input_tensor.contiguous()
        
        # Allocate output tensor
        output = torch.empty(n_channels, device=input_tensor.device, dtype=torch.float32)
        
        # Get raw data pointers
        input_ptr = input_tensor.data_ptr()
        output_ptr = output.data_ptr()
        
        # Get CUDA stream if provided
        stream_ptr = None
        if stream is not None:
            stream_ptr = ctypes.c_void_p(stream.cuda_stream)
        
        # Call the kernel
        result = self._lib.synthos_fused_spectral_entropy(
            ctypes.c_void_p(input_ptr),
            ctypes.c_void_p(output_ptr),
            ctypes.c_int(n_samples),
            ctypes.c_int(n_channels),
            stream_ptr
        )
        
        if result != 0:
            error_msg = self._lib.synthos_get_error_string(result).decode('utf-8')
            raise RuntimeError(
                f"Spectral entropy computation failed: {error_msg} "
                f"(input shape: {input_tensor.shape}, error: {result})"
            )
        
        return output
    
    def _validate_tensor(self, tensor: torch.Tensor):
        """Validate input tensor properties"""
        if not isinstance(tensor, torch.Tensor):
            raise ValueError(f"Expected torch.Tensor, got {type(tensor)}")
        
        if tensor.device.type != 'cuda':
            raise ValueError(
                f"Input tensor must be on CUDA device, got {tensor.device}. "
                f"Use tensor.cuda() to move to GPU."
            )
        
        if tensor.dtype != torch.float32:
            raise ValueError(
                f"Input tensor must be float32, got {tensor.dtype}. "
                f"Use tensor.float() to convert."
            )
        
        if tensor.dim() != 2:
            raise ValueError(
                f"Input tensor must be 2D [N_samples, N_channels], got {tensor.dim()}D"
            )
        
        if tensor.shape[0] < 4:
            raise ValueError(
                f"FFT size must be at least 4, got {tensor.shape[0]}"
            )
    
    @staticmethod
    def _next_power_of_2(n: int) -> int:
        """Get next power of 2 >= n"""
        if n <= 0:
            return 1
        n -= 1
        n |= n >> 1
        n |= n >> 2
        n |= n >> 4
        n |= n >> 8
        n |= n >> 16
        return n + 1
    
    @property
    def gpu_architecture(self) -> str:
        """Return detected GPU architecture"""
        return self.active_arch
    
    @property
    def device_id(self) -> int:
        """Return CUDA device ID"""
        return self._device_id
    
    def get_performance_info(self) -> Dict[str, Any]:
        """
        Return performance information for the detected GPU.
        
        Returns:
            Dict with keys:
                - compute_capability: (major, minor) tuple
                - arch: Architecture enum value
                - arch_name: Human-readable architecture name
                - expected_speedup: Estimated speedup vs PyTorch
                - global_memory_gb: GPU memory in GB
                - multiprocessors: Number of SMs
        """
        info = self._device_info
        return {
            "compute_capability": (info.compute_capability_major, info.compute_capability_minor),
            "arch": info.arch,
            "arch_name": SynthosArch.name(info.arch),
            "expected_speedup": info.expected_speedup_vs_pytorch,
            "global_memory_gb": info.global_memory_bytes / (1024**3),
            "multiprocessors": info.multiprocessor_count,
            "max_threads_per_block": info.max_threads_per_block,
            "warp_size": info.warp_size,
        }
    
    def get_workspace_size(self, n_samples: int, n_channels: int) -> Dict[str, int]:
        """
        Get workspace memory requirements for given dimensions.
        
        Args:
            n_samples: FFT size
            n_channels: Batch size
        
        Returns:
            Dict with workspace size in bytes
        """
        info = SynthosWorkspaceInfo()
        self._lib.synthos_get_workspace_size(n_samples, n_channels, ctypes.byref(info))
        return {
            "fft_workspace_bytes": info.fft_workspace_bytes,
            "reduction_workspace_bytes": info.reduction_workspace_bytes,
            "total_bytes": info.total_bytes,
        }
    
    def synchronize(self):
        """Synchronize all pending CUDA operations"""
        self._lib.synthos_synchronize()
    
    def cleanup(self):
        """Cleanup resources and release cuFFT plans"""
        if hasattr(self, '_lib') and self._lib:
            self._lib.synthos_cleanup()
            SynthOSKernel._initialized = False
    
    def __del__(self):
        """Destructor - cleanup on garbage collection"""
        # Don't cleanup in destructor to avoid issues with global cleanup order
        pass
    
    @staticmethod
    def get_version() -> str:
        """Get library version string"""
        if SynthOSKernel._instance and hasattr(SynthOSKernel._instance, '_lib'):
            version = SynthOSKernel._instance._lib.synthos_get_version()
            return version.decode('utf-8') if version else "Unknown"
        return "Not initialized"


# ==============================================================================
# Benchmark Utilities
# ==============================================================================

def benchmark_vs_pytorch(
    n_samples: int = 4096,
    n_channels: int = 128,
    warmup_iterations: int = 10,
    benchmark_iterations: int = 100,
    lib_path: str = "auto"
) -> Dict[str, Any]:
    """
    Benchmark SynthOS kernel against PyTorch baseline.
    
    Args:
        n_samples: FFT size (power of 2)
        n_channels: Batch size
        warmup_iterations: Number of warmup iterations
        benchmark_iterations: Number of timed iterations
        lib_path: Path to library
    
    Returns:
        Dict with timing results and speedup factor
    """
    import torch.nn.functional as F
    
    # Create test data
    input_tensor = torch.randn(n_samples, n_channels, device='cuda', dtype=torch.float32)
    
    # PyTorch baseline implementation
    def pytorch_spectral_entropy(x: torch.Tensor) -> torch.Tensor:
        fft = torch.fft.rfft(x, dim=0)
        psd = torch.abs(fft) ** 2
        psd_norm = F.normalize(psd, p=1, dim=0)
        entropy = -torch.sum(psd_norm * torch.log(psd_norm + 1e-10), dim=0)
        return entropy
    
    # Initialize kernel
    kernel = SynthOSKernel(lib_path)
    
    # Warmup
    for _ in range(warmup_iterations):
        _ = pytorch_spectral_entropy(input_tensor)
        _ = kernel.compute_spectral_entropy(input_tensor)
    torch.cuda.synchronize()
    
    # Benchmark PyTorch
    start = torch.cuda.Event(enable_timing=True)
    end = torch.cuda.Event(enable_timing=True)
    
    start.record()
    for _ in range(benchmark_iterations):
        pytorch_result = pytorch_spectral_entropy(input_tensor)
    end.record()
    torch.cuda.synchronize()
    pytorch_time_ms = start.elapsed_time(end) / benchmark_iterations
    
    # Benchmark SynthOS
    start.record()
    for _ in range(benchmark_iterations):
        synthos_result = kernel.compute_spectral_entropy(input_tensor)
    end.record()
    torch.cuda.synchronize()
    synthos_time_ms = start.elapsed_time(end) / benchmark_iterations
    
    # Verify numerical accuracy
    pytorch_final = pytorch_result.cpu()
    synthos_final = synthos_result.cpu()
    max_diff = (pytorch_final - synthos_final).abs().max().item()
    rel_diff = (pytorch_final - synthos_final).abs() / (pytorch_final.abs() + 1e-10)
    max_rel_diff = rel_diff.max().item()
    
    speedup = pytorch_time_ms / synthos_time_ms
    
    return {
        "n_samples": n_samples,
        "n_channels": n_channels,
        "pytorch_time_ms": pytorch_time_ms,
        "synthos_time_ms": synthos_time_ms,
        "speedup": speedup,
        "max_absolute_diff": max_diff,
        "max_relative_diff": max_rel_diff,
        "numerical_match": max_rel_diff < 1e-4,
        "gpu_architecture": kernel.active_arch,
        "expected_speedup": kernel.get_performance_info()["expected_speedup"],
    }


# ==============================================================================
# Module-Level Convenience Functions
# ==============================================================================

_global_kernel: Optional[SynthOSKernel] = None

def get_kernel(lib_path: str = "auto", device: int = 0) -> SynthOSKernel:
    """
    Get or create the global SynthOS kernel instance.
    
    This is a convenience function for singleton access.
    """
    global _global_kernel
    if _global_kernel is None:
        _global_kernel = SynthOSKernel(lib_path, device)
    return _global_kernel


def compute_spectral_entropy(
    input_tensor: torch.Tensor,
    stream: Optional[torch.cuda.Stream] = None
) -> torch.Tensor:
    """
    Convenience function to compute spectral entropy.
    
    Creates kernel on first call, reuses afterward.
    
    Args:
        input_tensor: [N, M] float32 CUDA tensor
        stream: Optional CUDA stream
    
    Returns:
        [M] entropy tensor
    """
    return get_kernel().compute_spectral_entropy(input_tensor, stream)


# ==============================================================================
# Main Entry Point (for testing)
# ==============================================================================

if __name__ == "__main__":
    import argparse
    
    parser = argparse.ArgumentParser(description="SynthOS Kernel Test")
    parser.add_argument("--lib", default="auto", help="Path to libsynthos.so")
    parser.add_argument("--benchmark", action="store_true", help="Run benchmark")
    parser.add_argument("--n-samples", type=int, default=4096, help="FFT size")
    parser.add_argument("--n-channels", type=int, default=128, help="Batch size")
    args = parser.parse_args()
    
    logging.basicConfig(level=logging.INFO)
    
    try:
        kernel = SynthOSKernel(args.lib)
        print(f"\n=== SynthOS Kernel Info ===")
        print(f"Version: {kernel.get_version()}")
        print(f"GPU Architecture: {kernel.active_arch}")
        print(f"Performance Info: {kernel.get_performance_info()}")
        
        if args.benchmark:
            print(f"\n=== Benchmark: {args.n_samples}x{args.n_channels} ===")
            results = benchmark_vs_pytorch(args.n_samples, args.n_channels, lib_path=args.lib)
            print(f"PyTorch time: {results['pytorch_time_ms']:.3f} ms")
            print(f"SynthOS time: {results['synthos_time_ms']:.3f} ms")
            print(f"Speedup: {results['speedup']:.2f}x")
            print(f"Max relative diff: {results['max_relative_diff']:.2e}")
            print(f"Numerical match: {'PASS' if results['numerical_match'] else 'FAIL'}")
        else:
            # Quick functionality test
            print("\n=== Quick Test ===")
            x = torch.randn(args.n_samples, args.n_channels, device='cuda', dtype=torch.float32)
            result = kernel.compute_spectral_entropy(x)
            print(f"Input shape: {x.shape}")
            print(f"Output shape: {result.shape}")
            print(f"Output mean: {result.mean():.4f}")
            print("Test: PASS")
            
    except Exception as e:
        print(f"Error: {e}")
        sys.exit(1)
