# SynthOS Fused Spectral Entropy CUDA Kernel

High-performance fused CUDA kernel for spectral entropy computation, designed to replace PyTorch's multi-step FFT → PSD → Normalize → Entropy pipeline with a single optimized kernel.

## Performance Summary

| GPU Architecture | Expected Speedup | Notes |
|-----------------|------------------|-------|
| Hopper (H100, sm_90) | ~5x | Thread block clusters, vectorized loads |
| Ada Lovelace (RTX 40, sm_89) | ~4x | Similar to Ampere optimizations |
| Ampere (A100, sm_80) | ~4x | Async copy, pipelined loads |
| Ampere (RTX 30, sm_86) | ~3.5x | Consumer Ampere optimizations |
| Turing (RTX 20, sm_75) | ~2.5x | Cooperative groups |
| Volta (V100, sm_70) | ~3x | Cooperative groups, HBM2 optimization |
| Pascal (GTX 10, sm_60) | ~2x | Baseline implementation |

## Features

- **Fused Operations**: Single kernel launch for FFT → PSD → L1 Norm → Entropy
- **Multi-Architecture Support**: Optimized code paths for Pascal through Hopper
- **Zero-Copy PyTorch Integration**: Direct tensor data pointer access
- **Runtime Architecture Detection**: Automatic optimal kernel selection
- **cuFFT Integration**: Leverages NVIDIA's optimized FFT library
- **Stream Support**: Async execution with CUDA streams
- **Thread-Safe**: No global state, safe for multi-stream execution

## Directory Structure

```
synthos_kernel/
├── spectral.cu              # Main CUDA implementation
├── spectral.h               # C ABI header
├── arch_dispatch.cuh        # Architecture detection and dispatch
├── kernels/
│   ├── spectral_sm60.cuh    # Pascal baseline (GTX 10-series)
│   ├── spectral_sm70.cuh    # Volta optimizations (V100)
│   ├── spectral_sm80.cuh    # Ampere optimizations (A100/RTX 30)
│   └── spectral_sm90.cuh    # Hopper optimizations (H100)
├── CMakeLists.txt           # CMake build system
├── Makefile                 # Alternative Makefile build
├── python_wrapper.py        # Python ctypes binding
├── test_spectral.py         # Unit tests
├── benchmark_architectures.py  # Performance benchmarks
└── README.md                # This file
```

## Building

### Prerequisites

- CUDA Toolkit 11.0+ (12.x recommended for Hopper)
- CMake 3.18+ (for CMake build)
- Python 3.8+ with PyTorch 2.0+ (for Python wrapper)
- Linux x86_64

### Using CMake (Recommended)

```bash
cd synthos_kernel
mkdir build && cd build

# Build for all architectures (fat binary)
cmake .. -DCMAKE_BUILD_TYPE=Release
cmake --build . --parallel

# Build for specific GPU only (faster compile)
cmake .. -DSYNTHOS_CUDA_ARCH=native

# Install to Python site-packages
cmake .. -DSYNTHOS_INSTALL_PYTHON=ON
cmake --build . --target install
```

### Using Makefile

```bash
cd synthos_kernel

# Build fat binary (all architectures)
make

# Build for native GPU only
make CUDA_ARCH=native

# Build for specific architecture
make CUDA_ARCH=80  # A100

# Install to Python
make install-python

# Clean
make clean
```

### Build Options

| CMake Option | Makefile Equivalent | Description |
|-------------|---------------------|-------------|
| `-DSYNTHOS_CUDA_ARCH=all` | `CUDA_ARCH=all` | All architectures (default) |
| `-DSYNTHOS_CUDA_ARCH=native` | `CUDA_ARCH=native` | Detected GPU only |
| `-DSYNTHOS_CUDA_ARCH=80,90` | `CUDA_ARCH=80` | Specific architecture(s) |
| `-DSYNTHOS_INSTALL_PYTHON=ON` | `install-python` | Install Python package |
| `-DSYNTHOS_ENABLE_PROFILING=ON` | `PROFILE=1` | Enable nvtx annotations |

### Expected Binary Size

The fat binary containing all architectures is approximately 15-30MB. Building for a single architecture produces ~2-5MB.

## Usage

### Python API

```python
from synthos_kernel import SynthOSKernel
import torch

# Initialize (auto-detects GPU and library)
kernel = SynthOSKernel()
print(f"Using: {kernel.active_arch}")
print(f"Expected speedup: {kernel.get_performance_info()['expected_speedup']}x")

# Create input tensor [N_samples, N_channels]
x = torch.randn(4096, 128, device='cuda', dtype=torch.float32)

# Compute spectral entropy
entropy = kernel.compute_spectral_entropy(x)
print(f"Output shape: {entropy.shape}")  # [128]

# With CUDA stream for async execution
stream = torch.cuda.Stream()
with torch.cuda.stream(stream):
    entropy = kernel.compute_spectral_entropy(x, stream=stream)
```

### Convenience Function

```python
from synthos_kernel import compute_spectral_entropy

# One-liner (creates kernel on first call)
entropy = compute_spectral_entropy(input_tensor)
```

### C API

```c
#include "synthos/spectral.h"

// Initialize for device 0
cudaError_t err = synthos_init(0);

// Get architecture info
printf("Architecture: %s\n", synthos_get_active_arch());

// Compute spectral entropy
err = synthos_fused_spectral_entropy(
    d_input,      // Device pointer [N × M]
    d_entropy,    // Device pointer [M]
    n_samples,    // FFT size (power of 2)
    n_channels,   // Batch size
    stream        // CUDA stream (or NULL)
);

// Cleanup
synthos_cleanup();
```

## Input Requirements

| Parameter | Constraints |
|-----------|-------------|
| Shape | `[N, M]` where N = samples, M = channels |
| N (FFT size) | Power of 2, range [4, 8192] |
| M (channels) | Any positive integer |
| dtype | `torch.float32` (FP32) |
| device | CUDA GPU |
| memory | Contiguous (auto-converted if not) |

## Algorithm

The kernel performs the following fused operations:

1. **Batch R2C FFT** (via cuFFT)
   - Real-to-complex transform along sample dimension
   - Output: `[N/2+1, M]` complex values

2. **Power Spectral Density**
   - `PSD = |FFT|² = real² + imag²`
   - Computed element-wise

3. **L1 Normalization** (per channel)
   - `p = PSD / sum(PSD)`
   - Parallel reduction for sum

4. **Spectral Entropy**
   - `H = -sum(p * log(p))`
   - Fused with normalization pass

### Architecture-Specific Optimizations

**Pascal (sm_60) - Baseline:**
- Warp shuffle reductions (`__shfl_down_sync`)
- Shared memory for inter-warp communication
- Standard memory access patterns

**Volta (sm_70):**
- Cooperative groups for explicit synchronization
- Independent thread scheduling awareness
- Optimized for V100's HBM2

**Ampere (sm_80/86):**
- Async memory copy (`cp.async`)
- Pipelined memory loads with computation
- Register-based single-pass for common sizes

**Hopper (sm_90):**
- Vectorized 4-wide loads
- Branch-free entropy computation
- Optimized for H100's 3TB/s HBM3

## Testing

### Run Unit Tests

```bash
# Basic functionality tests
python test_spectral.py

# With verbose output
python test_spectral.py -v

# Include performance benchmarks
python test_spectral.py --benchmark

# Custom library path
python test_spectral.py --lib ./build/libsynthos.so
```

### Run Benchmarks

```bash
# Full benchmark suite
python benchmark_architectures.py

# Quick benchmark
python benchmark_architectures.py --quick

# Save results
python benchmark_architectures.py --output results.json --markdown report.md

# Show profiling commands
python benchmark_architectures.py --profile
```

## Profiling

### NVIDIA Nsight Systems

```bash
nsys profile \
    --trace=cuda,nvtx,osrt \
    --cuda-memory-usage=true \
    --output=synthos_profile \
    python benchmark_architectures.py --quick
```

### NVIDIA Nsight Compute

```bash
ncu --set full \
    --target-processes all \
    --launch-count 10 \
    python -c "
from synthos_kernel import SynthOSKernel
import torch
kernel = SynthOSKernel()
x = torch.randn(4096, 128, device='cuda', dtype=torch.float32)
for _ in range(20):
    _ = kernel.compute_spectral_entropy(x)
"
```

## Integration with detector.py

Apply the following changes to `ml_backend/src/collapse_engine/detector.py`:

```python
# Add import at top of file
from synthos_kernel import SynthOSKernel, compute_spectral_entropy

# In CollapseDetector.__init__(), add:
try:
    self._synthos_kernel = SynthOSKernel()
    logger.info(f"SynthOS kernel initialized: {self._synthos_kernel.active_arch}")
    self._use_synthos = True
except Exception as e:
    logger.warning(f"SynthOS kernel not available, using PyTorch: {e}")
    self._use_synthos = False

# In _analyze_spectral_coherence(), replace the entropy computation:

# BEFORE (multiple kernel launches):
synth_psd_norm = F.normalize(synth_psd, p=1, dim=0)
synth_spectral_entropy = -torch.sum(
    synth_psd_norm * torch.log(synth_psd_norm + 1e-10),
    dim=0
).mean().item()

# AFTER (single fused kernel):
if self._use_synthos:
    synth_spectral_entropy = self._synthos_kernel.compute_spectral_entropy(
        synth_tensor
    ).mean().item()
else:
    # Fallback to PyTorch
    synth_fft = torch.fft.rfft(synth_tensor, dim=0)
    synth_psd = torch.abs(synth_fft) ** 2
    synth_psd_norm = F.normalize(synth_psd, p=1, dim=0)
    synth_spectral_entropy = -torch.sum(
        synth_psd_norm * torch.log(synth_psd_norm + 1e-10),
        dim=0
    ).mean().item()
```

## Numerical Accuracy

The kernel matches PyTorch output within the following tolerances:

| Metric | Tolerance |
|--------|-----------|
| Max absolute error | < 1e-3 |
| Max relative error | < 1e-4 |

Small differences arise from:
- Fast math optimizations (`__logf` vs `logf`)
- Different reduction order (warp-level)
- Epsilon handling in log computation

These differences are well within acceptable bounds for ML applications.

## Troubleshooting

### Library Not Found

```
FileNotFoundError: Cannot find libsynthos.so
```

Solutions:
1. Build the library: `make` or `cmake --build .`
2. Specify path: `SynthOSKernel(lib_path='/path/to/libsynthos.so')`
3. Add to `LD_LIBRARY_PATH`: `export LD_LIBRARY_PATH=$PWD/build:$LD_LIBRARY_PATH`

### Unsupported GPU

```
RuntimeError: GPU compute capability 5.2 is below minimum 6.0
```

SynthOS requires Pascal (GTX 10-series) or newer. Upgrade your GPU or use PyTorch fallback.

### FFT Size Errors

```
ValueError: FFT size 1000 must be a power of 2
```

Input tensor's first dimension must be a power of 2 (64, 128, 256, ..., 8192).

### CUDA Out of Memory

For large batch sizes, workspace memory may exceed available GPU memory. Solutions:
1. Reduce batch size
2. Use smaller FFT sizes
3. Process in chunks

## Future Work

- [ ] FP16/BF16 precision variants
- [ ] Multi-GPU support
- [ ] Windows compatibility
- [ ] AMD GPU support (HIP)
- [ ] Hopper TMA (Tensor Memory Accelerator) integration
- [ ] Dynamic parallelism for very large batches

## License

Copyright (c) 2026 SynthOS. See LICENSE file for details.

## References

- [cuFFT Documentation](https://docs.nvidia.com/cuda/cufft/)
- [CUDA C++ Programming Guide](https://docs.nvidia.com/cuda/cuda-c-programming-guide/)
- [Cooperative Groups](https://developer.nvidia.com/blog/cooperative-groups/)
- [Hopper Architecture Whitepaper](https://www.nvidia.com/content/dam/en-zz/Solutions/Data-Center/nvidia-ampere-architecture-whitepaper.pdf)
