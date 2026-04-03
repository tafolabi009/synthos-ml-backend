#!/usr/bin/env python3
"""
SynthOS Kernel - Cross-GPU Architecture Performance Comparison

This script benchmarks the SynthOS kernel across different GPU architectures
and generates a comprehensive performance report.

Usage:
    python benchmark_architectures.py --lib ./build/libsynthos.so
    python benchmark_architectures.py --output results.json
    python benchmark_architectures.py --profile  # Enable nsys profiling

Requirements:
    - PyTorch >= 2.0 with CUDA
    - libsynthos.so built for target GPU
    - (Optional) NVIDIA Nsight Systems for profiling

Author: SynthOS ML Backend Team
"""

import sys
import json
import time
import argparse
import subprocess
from pathlib import Path
from datetime import datetime
from typing import Dict, List, Any, Optional
from dataclasses import dataclass, asdict

import torch
import torch.nn.functional as F

# Add parent directory
sys.path.insert(0, str(Path(__file__).parent))


# ==============================================================================
# Configuration
# ==============================================================================

@dataclass
class BenchmarkConfig:
    """Benchmark configuration"""
    warmup_iterations: int = 20
    benchmark_iterations: int = 100
    fft_sizes: List[int] = None
    channel_counts: List[int] = None
    
    def __post_init__(self):
        if self.fft_sizes is None:
            self.fft_sizes = [512, 1024, 2048, 4096, 8192]
        if self.channel_counts is None:
            self.channel_counts = [32, 64, 128, 256]


@dataclass
class BenchmarkResult:
    """Single benchmark result"""
    n_samples: int
    n_channels: int
    pytorch_time_ms: float
    synthos_time_ms: float
    speedup: float
    memory_mb: float
    numerical_error: float


# ==============================================================================
# Reference Implementation
# ==============================================================================

def pytorch_spectral_entropy(x: torch.Tensor) -> torch.Tensor:
    """PyTorch baseline implementation"""
    fft = torch.fft.rfft(x, dim=0)
    psd = torch.abs(fft) ** 2
    psd_norm = F.normalize(psd, p=1, dim=0)
    entropy = -torch.sum(psd_norm * torch.log(psd_norm + 1e-10), dim=0)
    return entropy


# ==============================================================================
# Benchmark Functions
# ==============================================================================

def benchmark_single(
    kernel,
    n_samples: int,
    n_channels: int,
    config: BenchmarkConfig
) -> BenchmarkResult:
    """
    Run benchmark for a single configuration.
    
    Returns detailed timing and accuracy information.
    """
    # Create input data
    x = torch.randn(n_samples, n_channels, device='cuda', dtype=torch.float32)
    
    # Measure memory before
    torch.cuda.synchronize()
    torch.cuda.reset_peak_memory_stats()
    
    # Warmup
    for _ in range(config.warmup_iterations):
        _ = pytorch_spectral_entropy(x)
        _ = kernel.compute_spectral_entropy(x)
    torch.cuda.synchronize()
    
    # Benchmark PyTorch
    start = torch.cuda.Event(enable_timing=True)
    end = torch.cuda.Event(enable_timing=True)
    
    start.record()
    for _ in range(config.benchmark_iterations):
        pytorch_result = pytorch_spectral_entropy(x)
    end.record()
    torch.cuda.synchronize()
    pytorch_time = start.elapsed_time(end) / config.benchmark_iterations
    
    # Benchmark SynthOS
    start.record()
    for _ in range(config.benchmark_iterations):
        synthos_result = kernel.compute_spectral_entropy(x)
    end.record()
    torch.cuda.synchronize()
    synthos_time = start.elapsed_time(end) / config.benchmark_iterations
    
    # Memory usage
    memory_mb = torch.cuda.max_memory_allocated() / (1024 * 1024)
    
    # Numerical accuracy
    max_error = (pytorch_result - synthos_result).abs().max().item()
    
    return BenchmarkResult(
        n_samples=n_samples,
        n_channels=n_channels,
        pytorch_time_ms=pytorch_time,
        synthos_time_ms=synthos_time,
        speedup=pytorch_time / synthos_time,
        memory_mb=memory_mb,
        numerical_error=max_error
    )


def run_full_benchmark(
    kernel,
    config: BenchmarkConfig
) -> Dict[str, Any]:
    """
    Run full benchmark suite across all configurations.
    
    Returns comprehensive results dictionary.
    """
    results = {
        "metadata": {
            "timestamp": datetime.now().isoformat(),
            "gpu_name": torch.cuda.get_device_name(0),
            "gpu_architecture": kernel.active_arch,
            "cuda_version": torch.version.cuda,
            "pytorch_version": torch.__version__,
            "kernel_version": kernel.get_version(),
            "config": asdict(config)
        },
        "device_info": kernel.get_performance_info(),
        "benchmarks": [],
        "summary": {}
    }
    
    print("\n" + "=" * 80)
    print(f"SynthOS Kernel Benchmark")
    print(f"GPU: {results['metadata']['gpu_name']}")
    print(f"Architecture: {kernel.active_arch}")
    print("=" * 80)
    
    all_speedups = []
    
    for n_samples in config.fft_sizes:
        print(f"\nFFT Size: {n_samples}")
        print("-" * 60)
        print(f"{'Channels':>10} | {'PyTorch':>12} | {'SynthOS':>12} | {'Speedup':>10} | {'Error':>10}")
        print("-" * 60)
        
        for n_channels in config.channel_counts:
            result = benchmark_single(kernel, n_samples, n_channels, config)
            results["benchmarks"].append(asdict(result))
            all_speedups.append(result.speedup)
            
            error_str = f"{result.numerical_error:.2e}"
            print(
                f"{n_channels:>10} | "
                f"{result.pytorch_time_ms:>10.3f}ms | "
                f"{result.synthos_time_ms:>10.3f}ms | "
                f"{result.speedup:>9.2f}x | "
                f"{error_str:>10}"
            )
    
    # Summary statistics
    results["summary"] = {
        "min_speedup": min(all_speedups),
        "max_speedup": max(all_speedups),
        "avg_speedup": sum(all_speedups) / len(all_speedups),
        "expected_speedup": results["device_info"]["expected_speedup"],
        "total_benchmarks": len(all_speedups),
        "meets_target": sum(all_speedups) / len(all_speedups) >= 2.0
    }
    
    print("\n" + "=" * 80)
    print("Summary")
    print("-" * 80)
    print(f"Average Speedup: {results['summary']['avg_speedup']:.2f}x")
    print(f"Min Speedup: {results['summary']['min_speedup']:.2f}x")
    print(f"Max Speedup: {results['summary']['max_speedup']:.2f}x")
    print(f"Expected Speedup: {results['summary']['expected_speedup']:.1f}x")
    print(f"Meets 2x Target: {'YES ✓' if results['summary']['meets_target'] else 'NO ✗'}")
    print("=" * 80)
    
    return results


# ==============================================================================
# Profiling Support
# ==============================================================================

def generate_nsys_command(lib_path: str) -> str:
    """Generate nsys profiling command"""
    script = Path(__file__).resolve()
    return f"""
# Profile with NVIDIA Nsight Systems
nsys profile \\
    --trace=cuda,nvtx,osrt \\
    --cuda-memory-usage=true \\
    --output=synthos_profile \\
    python {script} --lib {lib_path} --no-profile
    
# View the report
nsys stats synthos_profile.nsys-rep

# Or open in Nsight Systems GUI:
# nsys-ui synthos_profile.nsys-rep
"""


def generate_ncu_command(lib_path: str) -> str:
    """Generate ncu (Nsight Compute) profiling command"""
    return f"""
# Profile kernel with NVIDIA Nsight Compute
ncu --set full \\
    --target-processes all \\
    --launch-count 10 \\
    python -c "
import torch
import sys
sys.path.insert(0, '.')
from python_wrapper import SynthOSKernel
kernel = SynthOSKernel('{lib_path}')
x = torch.randn(4096, 128, device='cuda', dtype=torch.float32)
for _ in range(20):
    _ = kernel.compute_spectral_entropy(x)
"
"""


# ==============================================================================
# Comparison Report
# ==============================================================================

def generate_comparison_table(results: Dict[str, Any]) -> str:
    """Generate markdown comparison table"""
    lines = [
        "# SynthOS Kernel Performance Report",
        "",
        f"**Date:** {results['metadata']['timestamp']}",
        f"**GPU:** {results['metadata']['gpu_name']}",
        f"**Architecture:** {results['metadata']['gpu_architecture']}",
        f"**CUDA Version:** {results['metadata']['cuda_version']}",
        "",
        "## Performance Summary",
        "",
        f"| Metric | Value |",
        f"|--------|-------|",
        f"| Average Speedup | {results['summary']['avg_speedup']:.2f}x |",
        f"| Min Speedup | {results['summary']['min_speedup']:.2f}x |",
        f"| Max Speedup | {results['summary']['max_speedup']:.2f}x |",
        f"| Expected Speedup | {results['summary']['expected_speedup']:.1f}x |",
        f"| Meets 2x Target | {'✓ Yes' if results['summary']['meets_target'] else '✗ No'} |",
        "",
        "## Detailed Results",
        "",
        "| FFT Size | Channels | PyTorch (ms) | SynthOS (ms) | Speedup | Error |",
        "|----------|----------|--------------|--------------|---------|-------|",
    ]
    
    for b in results['benchmarks']:
        lines.append(
            f"| {b['n_samples']} | {b['n_channels']} | "
            f"{b['pytorch_time_ms']:.3f} | {b['synthos_time_ms']:.3f} | "
            f"{b['speedup']:.2f}x | {b['numerical_error']:.2e} |"
        )
    
    lines.extend([
        "",
        "## Architecture-Specific Optimizations",
        "",
    ])
    
    arch = results['metadata']['gpu_architecture']
    if 'Hopper' in arch or 'sm_90' in arch:
        lines.append("- **Hopper (H100):** Thread block clusters, vectorized loads, optimized reductions")
    elif 'Ampere' in arch or 'sm_80' in arch or 'sm_86' in arch:
        lines.append("- **Ampere:** Async memory copy, pipelined loads, improved warp reductions")
    elif 'Volta' in arch or 'sm_70' in arch:
        lines.append("- **Volta:** Cooperative groups, optimized shared memory access")
    else:
        lines.append("- **Pascal:** Baseline warp shuffle reductions, shared memory optimization")
    
    return "\n".join(lines)


# ==============================================================================
# Main
# ==============================================================================

def main():
    parser = argparse.ArgumentParser(
        description="SynthOS Kernel Architecture Benchmark"
    )
    parser.add_argument(
        "--lib", default="auto",
        help="Path to libsynthos.so"
    )
    parser.add_argument(
        "--output", default=None,
        help="Output JSON file path"
    )
    parser.add_argument(
        "--markdown", default=None,
        help="Output markdown report path"
    )
    parser.add_argument(
        "--warmup", type=int, default=20,
        help="Warmup iterations"
    )
    parser.add_argument(
        "--iterations", type=int, default=100,
        help="Benchmark iterations"
    )
    parser.add_argument(
        "--profile", action="store_true",
        help="Show profiling commands"
    )
    parser.add_argument(
        "--quick", action="store_true",
        help="Quick benchmark (fewer configurations)"
    )
    args = parser.parse_args()
    
    # Show profiling commands if requested
    if args.profile:
        print("\n=== Nsight Systems Profiling ===")
        print(generate_nsys_command(args.lib))
        print("\n=== Nsight Compute Profiling ===")
        print(generate_ncu_command(args.lib))
        return
    
    # Check CUDA
    if not torch.cuda.is_available():
        print("Error: CUDA not available")
        sys.exit(1)
    
    # Initialize kernel
    try:
        from python_wrapper import SynthOSKernel
        kernel = SynthOSKernel(args.lib)
    except Exception as e:
        print(f"Error loading kernel: {e}")
        sys.exit(1)
    
    # Configure benchmark
    if args.quick:
        config = BenchmarkConfig(
            warmup_iterations=5,
            benchmark_iterations=20,
            fft_sizes=[1024, 4096],
            channel_counts=[64, 128]
        )
    else:
        config = BenchmarkConfig(
            warmup_iterations=args.warmup,
            benchmark_iterations=args.iterations
        )
    
    # Run benchmark
    results = run_full_benchmark(kernel, config)
    
    # Save JSON output
    if args.output:
        with open(args.output, 'w') as f:
            json.dump(results, f, indent=2)
        print(f"\nResults saved to: {args.output}")
    
    # Generate markdown report
    if args.markdown:
        report = generate_comparison_table(results)
        with open(args.markdown, 'w') as f:
            f.write(report)
        print(f"Report saved to: {args.markdown}")
    
    # Exit code based on target
    sys.exit(0 if results['summary']['meets_target'] else 1)


if __name__ == "__main__":
    main()
