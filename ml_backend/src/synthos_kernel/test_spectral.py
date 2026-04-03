#!/usr/bin/env python3
"""
SynthOS Spectral Entropy Kernel - Test Suite

Comprehensive tests for:
    - Numerical accuracy vs PyTorch baseline
    - Performance benchmarking
    - Edge cases and error handling
    - Multi-architecture validation

Usage:
    python test_spectral.py                    # Run all tests
    python test_spectral.py --benchmark        # Include performance benchmarks
    python test_spectral.py --lib /path/to.so  # Custom library path
    python test_spectral.py -v                 # Verbose output

Author: SynthOS ML Backend Team
"""

import sys
import unittest
import logging
from pathlib import Path
from typing import Tuple, Optional
import time

import numpy as np
import torch
import torch.nn.functional as F

# Add parent directory to path for imports
sys.path.insert(0, str(Path(__file__).parent))

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)


# ==============================================================================
# PyTorch Reference Implementation
# ==============================================================================

def pytorch_spectral_entropy(x: torch.Tensor) -> torch.Tensor:
    """
    Reference implementation using PyTorch operations.
    
    This is the original implementation we're optimizing.
    
    Args:
        x: Input tensor [N, M]
    
    Returns:
        Per-channel spectral entropy [M]
    """
    # FFT
    fft = torch.fft.rfft(x, dim=0)
    
    # Power spectral density
    psd = torch.abs(fft) ** 2
    
    # L1 normalize
    psd_norm = F.normalize(psd, p=1, dim=0)
    
    # Spectral entropy
    entropy = -torch.sum(psd_norm * torch.log(psd_norm + 1e-10), dim=0)
    
    return entropy


def pytorch_spectral_entropy_decomposed(x: torch.Tensor) -> Tuple[torch.Tensor, dict]:
    """
    Decomposed reference implementation for debugging.
    
    Returns intermediate values for validation.
    """
    intermediates = {}
    
    # Step 1: FFT
    fft = torch.fft.rfft(x, dim=0)
    intermediates['fft_real'] = fft.real.clone()
    intermediates['fft_imag'] = fft.imag.clone()
    
    # Step 2: PSD
    psd = torch.abs(fft) ** 2
    intermediates['psd'] = psd.clone()
    
    # Step 3: Normalize
    psd_sum = psd.sum(dim=0, keepdim=True)
    psd_norm = psd / (psd_sum + 1e-10)
    intermediates['psd_norm'] = psd_norm.clone()
    intermediates['psd_sum'] = psd_sum.squeeze()
    
    # Step 4: Entropy
    log_psd = torch.log(psd_norm + 1e-10)
    entropy_terms = -psd_norm * log_psd
    entropy = entropy_terms.sum(dim=0)
    intermediates['entropy_terms'] = entropy_terms.clone()
    
    return entropy, intermediates


# ==============================================================================
# Test Cases
# ==============================================================================

class TestSpectralEntropy(unittest.TestCase):
    """Test cases for SynthOS spectral entropy kernel"""
    
    @classmethod
    def setUpClass(cls):
        """Initialize kernel and check GPU availability"""
        if not torch.cuda.is_available():
            raise unittest.SkipTest("CUDA not available")
        
        # Try to load the kernel
        try:
            from python_wrapper import SynthOSKernel
            cls.kernel = SynthOSKernel(lib_path=getattr(cls, 'lib_path', 'auto'))
            logger.info(f"Loaded kernel: {cls.kernel.active_arch}")
        except Exception as e:
            logger.warning(f"Could not load SynthOS kernel: {e}")
            cls.kernel = None
    
    def setUp(self):
        """Check kernel availability before each test"""
        if self.kernel is None:
            self.skipTest("SynthOS kernel not available")
        torch.cuda.synchronize()
    
    # --------------------------------------------------------------------------
    # Numerical Accuracy Tests
    # --------------------------------------------------------------------------
    
    def test_basic_accuracy(self):
        """Test basic numerical accuracy against PyTorch"""
        n_samples, n_channels = 1024, 32
        x = torch.randn(n_samples, n_channels, device='cuda', dtype=torch.float32)
        
        pytorch_result = pytorch_spectral_entropy(x)
        synthos_result = self.kernel.compute_spectral_entropy(x)
        
        # Check shapes match
        self.assertEqual(pytorch_result.shape, synthos_result.shape)
        
        # Check numerical accuracy
        max_diff = (pytorch_result - synthos_result).abs().max().item()
        self.assertLess(max_diff, 1e-3, f"Max difference {max_diff} exceeds tolerance")
    
    def test_accuracy_various_sizes(self):
        """Test accuracy across various FFT sizes"""
        sizes = [64, 128, 256, 512, 1024, 2048, 4096]
        n_channels = 64
        
        for n_samples in sizes:
            with self.subTest(n_samples=n_samples):
                x = torch.randn(n_samples, n_channels, device='cuda', dtype=torch.float32)
                
                pytorch_result = pytorch_spectral_entropy(x)
                synthos_result = self.kernel.compute_spectral_entropy(x)
                
                rel_diff = (pytorch_result - synthos_result).abs() / (pytorch_result.abs() + 1e-10)
                max_rel_diff = rel_diff.max().item()
                
                self.assertLess(
                    max_rel_diff, 1e-4,
                    f"Size {n_samples}: relative difference {max_rel_diff} exceeds tolerance"
                )
    
    def test_accuracy_various_channels(self):
        """Test accuracy across various batch sizes"""
        n_samples = 2048
        channel_counts = [1, 8, 32, 64, 128, 256]
        
        for n_channels in channel_counts:
            with self.subTest(n_channels=n_channels):
                x = torch.randn(n_samples, n_channels, device='cuda', dtype=torch.float32)
                
                pytorch_result = pytorch_spectral_entropy(x)
                synthos_result = self.kernel.compute_spectral_entropy(x)
                
                max_diff = (pytorch_result - synthos_result).abs().max().item()
                self.assertLess(max_diff, 1e-3)
    
    def test_deterministic_output(self):
        """Test that same input produces same output"""
        x = torch.randn(1024, 32, device='cuda', dtype=torch.float32)
        
        result1 = self.kernel.compute_spectral_entropy(x)
        result2 = self.kernel.compute_spectral_entropy(x)
        
        self.assertTrue(
            torch.allclose(result1, result2),
            "Non-deterministic output detected"
        )
    
    def test_constant_input(self):
        """Test with constant input (edge case)"""
        x = torch.ones(1024, 16, device='cuda', dtype=torch.float32)
        
        pytorch_result = pytorch_spectral_entropy(x)
        synthos_result = self.kernel.compute_spectral_entropy(x)
        
        max_diff = (pytorch_result - synthos_result).abs().max().item()
        self.assertLess(max_diff, 1e-3)
    
    def test_near_zero_input(self):
        """Test with near-zero values (numerical stability)"""
        x = torch.randn(1024, 16, device='cuda', dtype=torch.float32) * 1e-6
        
        pytorch_result = pytorch_spectral_entropy(x)
        synthos_result = self.kernel.compute_spectral_entropy(x)
        
        # Both should produce valid (non-NaN, non-Inf) results
        self.assertFalse(torch.isnan(synthos_result).any(), "NaN in output")
        self.assertFalse(torch.isinf(synthos_result).any(), "Inf in output")
    
    def test_large_values(self):
        """Test with large values"""
        x = torch.randn(1024, 16, device='cuda', dtype=torch.float32) * 1e6
        
        pytorch_result = pytorch_spectral_entropy(x)
        synthos_result = self.kernel.compute_spectral_entropy(x)
        
        # Results should be similar (entropy is scale-invariant for normalized PSD)
        self.assertFalse(torch.isnan(synthos_result).any(), "NaN in output")
    
    # --------------------------------------------------------------------------
    # Error Handling Tests
    # --------------------------------------------------------------------------
    
    def test_error_non_cuda_tensor(self):
        """Test error on CPU tensor"""
        x = torch.randn(1024, 32, dtype=torch.float32)  # CPU tensor
        
        with self.assertRaises(ValueError) as ctx:
            self.kernel.compute_spectral_entropy(x)
        
        self.assertIn("CUDA", str(ctx.exception))
    
    def test_error_wrong_dtype(self):
        """Test error on non-float32 tensor"""
        x = torch.randn(1024, 32, device='cuda', dtype=torch.float64)
        
        with self.assertRaises(ValueError) as ctx:
            self.kernel.compute_spectral_entropy(x)
        
        self.assertIn("float32", str(ctx.exception))
    
    def test_error_non_power_of_2(self):
        """Test error on non-power-of-2 FFT size"""
        x = torch.randn(1000, 32, device='cuda', dtype=torch.float32)
        
        with self.assertRaises(ValueError) as ctx:
            self.kernel.compute_spectral_entropy(x)
        
        self.assertIn("power of 2", str(ctx.exception))
    
    def test_error_too_large_fft(self):
        """Test error on FFT size > 8192"""
        x = torch.randn(16384, 32, device='cuda', dtype=torch.float32)
        
        with self.assertRaises(ValueError) as ctx:
            self.kernel.compute_spectral_entropy(x)
        
        self.assertIn("8192", str(ctx.exception))
    
    def test_error_wrong_dimensions(self):
        """Test error on wrong tensor dimensions"""
        x = torch.randn(1024, device='cuda', dtype=torch.float32)  # 1D tensor
        
        with self.assertRaises(ValueError) as ctx:
            self.kernel.compute_spectral_entropy(x)
        
        self.assertIn("2D", str(ctx.exception))
    
    # --------------------------------------------------------------------------
    # Edge Cases
    # --------------------------------------------------------------------------
    
    def test_single_channel(self):
        """Test with single channel"""
        x = torch.randn(1024, 1, device='cuda', dtype=torch.float32)
        
        pytorch_result = pytorch_spectral_entropy(x)
        synthos_result = self.kernel.compute_spectral_entropy(x)
        
        self.assertEqual(synthos_result.shape, (1,))
        max_diff = (pytorch_result - synthos_result).abs().max().item()
        self.assertLess(max_diff, 1e-3)
    
    def test_minimum_fft_size(self):
        """Test with minimum FFT size"""
        x = torch.randn(4, 16, device='cuda', dtype=torch.float32)
        
        # Should not raise error
        result = self.kernel.compute_spectral_entropy(x)
        self.assertEqual(result.shape, (16,))
    
    def test_maximum_fft_size(self):
        """Test with maximum FFT size"""
        x = torch.randn(8192, 32, device='cuda', dtype=torch.float32)
        
        pytorch_result = pytorch_spectral_entropy(x)
        synthos_result = self.kernel.compute_spectral_entropy(x)
        
        max_diff = (pytorch_result - synthos_result).abs().max().item()
        self.assertLess(max_diff, 1e-3)
    
    def test_non_contiguous_input(self):
        """Test that non-contiguous input is handled"""
        x = torch.randn(2048, 64, device='cuda', dtype=torch.float32)
        x_non_contig = x[:, ::2]  # Non-contiguous slice
        
        self.assertFalse(x_non_contig.is_contiguous())
        
        pytorch_result = pytorch_spectral_entropy(x_non_contig.contiguous())
        synthos_result = self.kernel.compute_spectral_entropy(x_non_contig)
        
        max_diff = (pytorch_result - synthos_result).abs().max().item()
        self.assertLess(max_diff, 1e-3)


class TestKernelInfo(unittest.TestCase):
    """Test kernel information and utilities"""
    
    @classmethod
    def setUpClass(cls):
        if not torch.cuda.is_available():
            raise unittest.SkipTest("CUDA not available")
        
        try:
            from python_wrapper import SynthOSKernel
            cls.kernel = SynthOSKernel(lib_path=getattr(cls, 'lib_path', 'auto'))
        except Exception as e:
            cls.kernel = None
    
    def setUp(self):
        if self.kernel is None:
            self.skipTest("SynthOS kernel not available")
    
    def test_get_active_arch(self):
        """Test architecture detection"""
        arch = self.kernel.active_arch
        self.assertIsInstance(arch, str)
        self.assertIn("sm_", arch)
        logger.info(f"Detected architecture: {arch}")
    
    def test_get_performance_info(self):
        """Test performance info retrieval"""
        info = self.kernel.get_performance_info()
        
        self.assertIn("compute_capability", info)
        self.assertIn("expected_speedup", info)
        self.assertIn("global_memory_gb", info)
        
        # Validate values
        cc = info["compute_capability"]
        self.assertGreaterEqual(cc[0] * 10 + cc[1], 60)
        self.assertGreater(info["expected_speedup"], 1.0)
        self.assertGreater(info["global_memory_gb"], 0)
        
        logger.info(f"Performance info: {info}")
    
    def test_get_workspace_size(self):
        """Test workspace size calculation"""
        ws = self.kernel.get_workspace_size(4096, 128)
        
        self.assertIn("total_bytes", ws)
        self.assertGreater(ws["total_bytes"], 0)
        
        logger.info(f"Workspace for 4096x128: {ws['total_bytes'] / 1024:.1f} KB")
    
    def test_version_string(self):
        """Test version string"""
        from python_wrapper import SynthOSKernel
        version = SynthOSKernel.get_version()
        
        self.assertIsInstance(version, str)
        self.assertIn(".", version)  # Should contain version number
        logger.info(f"Version: {version}")


# ==============================================================================
# Performance Benchmarks
# ==============================================================================

class TestPerformance(unittest.TestCase):
    """Performance benchmarks"""
    
    @classmethod
    def setUpClass(cls):
        if not torch.cuda.is_available():
            raise unittest.SkipTest("CUDA not available")
        
        try:
            from python_wrapper import SynthOSKernel
            cls.kernel = SynthOSKernel(lib_path=getattr(cls, 'lib_path', 'auto'))
        except Exception:
            cls.kernel = None
    
    def setUp(self):
        if self.kernel is None:
            self.skipTest("SynthOS kernel not available")
    
    def _benchmark(
        self,
        n_samples: int,
        n_channels: int,
        warmup: int = 10,
        iterations: int = 100
    ) -> dict:
        """Run benchmark for given dimensions"""
        x = torch.randn(n_samples, n_channels, device='cuda', dtype=torch.float32)
        
        # Warmup
        for _ in range(warmup):
            _ = pytorch_spectral_entropy(x)
            _ = self.kernel.compute_spectral_entropy(x)
        torch.cuda.synchronize()
        
        # Benchmark PyTorch
        start = torch.cuda.Event(enable_timing=True)
        end = torch.cuda.Event(enable_timing=True)
        
        start.record()
        for _ in range(iterations):
            _ = pytorch_spectral_entropy(x)
        end.record()
        torch.cuda.synchronize()
        pytorch_ms = start.elapsed_time(end) / iterations
        
        # Benchmark SynthOS
        start.record()
        for _ in range(iterations):
            _ = self.kernel.compute_spectral_entropy(x)
        end.record()
        torch.cuda.synchronize()
        synthos_ms = start.elapsed_time(end) / iterations
        
        speedup = pytorch_ms / synthos_ms
        
        return {
            "n_samples": n_samples,
            "n_channels": n_channels,
            "pytorch_ms": pytorch_ms,
            "synthos_ms": synthos_ms,
            "speedup": speedup,
        }
    
    def test_benchmark_default(self):
        """Benchmark default workload (4096 × 128)"""
        result = self._benchmark(4096, 128)
        
        logger.info(
            f"Default (4096×128): PyTorch={result['pytorch_ms']:.3f}ms, "
            f"SynthOS={result['synthos_ms']:.3f}ms, "
            f"Speedup={result['speedup']:.2f}x"
        )
        
        # Should achieve at least 1.5x speedup
        self.assertGreater(
            result['speedup'], 1.5,
            f"Speedup {result['speedup']:.2f}x below minimum threshold"
        )
    
    def test_benchmark_sweep(self):
        """Benchmark across various workload sizes"""
        results = []
        
        test_cases = [
            (1024, 32),
            (1024, 128),
            (2048, 64),
            (2048, 256),
            (4096, 128),
            (8192, 64),
        ]
        
        print("\n" + "=" * 70)
        print("Performance Benchmark Results")
        print("=" * 70)
        print(f"GPU: {self.kernel.active_arch}")
        print("-" * 70)
        print(f"{'Size':>12} | {'PyTorch':>12} | {'SynthOS':>12} | {'Speedup':>10}")
        print("-" * 70)
        
        for n_samples, n_channels in test_cases:
            result = self._benchmark(n_samples, n_channels, warmup=5, iterations=50)
            results.append(result)
            
            print(
                f"{n_samples}×{n_channels:>3} | "
                f"{result['pytorch_ms']:>10.3f}ms | "
                f"{result['synthos_ms']:>10.3f}ms | "
                f"{result['speedup']:>8.2f}x"
            )
        
        print("-" * 70)
        
        avg_speedup = sum(r['speedup'] for r in results) / len(results)
        print(f"Average speedup: {avg_speedup:.2f}x")
        print("=" * 70)
        
        # Average should meet target
        self.assertGreater(avg_speedup, 2.0, "Average speedup below 2x target")


# ==============================================================================
# Main Entry Point
# ==============================================================================

def main():
    import argparse
    
    parser = argparse.ArgumentParser(description="SynthOS Kernel Test Suite")
    parser.add_argument("--lib", default="auto", help="Path to libsynthos.so")
    parser.add_argument("--benchmark", action="store_true", help="Include benchmarks")
    parser.add_argument("-v", "--verbose", action="store_true", help="Verbose output")
    args = parser.parse_args()
    
    # Set library path for test classes
    TestSpectralEntropy.lib_path = args.lib
    TestKernelInfo.lib_path = args.lib
    TestPerformance.lib_path = args.lib
    
    # Configure verbosity
    verbosity = 2 if args.verbose else 1
    
    # Create test suite
    loader = unittest.TestLoader()
    suite = unittest.TestSuite()
    
    # Always run accuracy and error handling tests
    suite.addTests(loader.loadTestsFromTestCase(TestSpectralEntropy))
    suite.addTests(loader.loadTestsFromTestCase(TestKernelInfo))
    
    # Optionally include benchmarks
    if args.benchmark:
        suite.addTests(loader.loadTestsFromTestCase(TestPerformance))
    
    # Run tests
    runner = unittest.TextTestRunner(verbosity=verbosity)
    result = runner.run(suite)
    
    # Return exit code
    sys.exit(0 if result.wasSuccessful() else 1)


if __name__ == "__main__":
    main()
