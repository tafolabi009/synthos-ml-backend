/**
 * @file spectral.h
 * @brief SynthOS Fused Spectral Entropy Kernel - C ABI Interface
 * 
 * High-performance fused CUDA kernel for spectral entropy computation.
 * Replaces multi-step PyTorch FFT → PSD → Normalize → Entropy pipeline
 * with a single optimized kernel supporting multiple GPU architectures.
 * 
 * Architecture Support:
 *   - Pascal (sm_60): GTX 10-series baseline
 *   - Volta (sm_70): V100 with cooperative groups
 *   - Turing (sm_75): RTX 20-series
 *   - Ampere (sm_80/86): A100/RTX 30-series with async copy
 *   - Ada Lovelace (sm_89): RTX 40-series
 *   - Hopper (sm_90): H100 with thread block clusters
 * 
 * @author SynthOS ML Backend Team
 * @version 1.0.0
 * @copyright (c) 2026 SynthOS
 */

#ifndef SYNTHOS_SPECTRAL_H
#define SYNTHOS_SPECTRAL_H

#ifdef __cplusplus
extern "C" {
#endif

#include <cuda_runtime.h>

/* Version information */
#define SYNTHOS_VERSION_MAJOR 1
#define SYNTHOS_VERSION_MINOR 0
#define SYNTHOS_VERSION_PATCH 0
#define SYNTHOS_VERSION_STRING "1.0.0"

/* Minimum supported compute capability */
#define SYNTHOS_MIN_COMPUTE_CAPABILITY 60

/* Maximum supported FFT size (power of 2) */
#define SYNTHOS_MAX_FFT_SIZE 8192

/* Error codes (extend cudaError_t) */
typedef enum {
    SYNTHOS_SUCCESS = 0,
    SYNTHOS_ERROR_INVALID_DEVICE = 1001,
    SYNTHOS_ERROR_UNSUPPORTED_ARCH = 1002,
    SYNTHOS_ERROR_INVALID_DIMENSIONS = 1003,
    SYNTHOS_ERROR_FFT_SIZE_NOT_POWER_OF_2 = 1004,
    SYNTHOS_ERROR_FFT_SIZE_TOO_LARGE = 1005,
    SYNTHOS_ERROR_NULL_POINTER = 1006,
    SYNTHOS_ERROR_CUFFT_FAILED = 1007,
    SYNTHOS_ERROR_NOT_INITIALIZED = 1008,
    SYNTHOS_ERROR_ALREADY_INITIALIZED = 1009,
    SYNTHOS_ERROR_WORKSPACE_TOO_SMALL = 1010,
    SYNTHOS_ERROR_CUDA_ERROR = 1011
} SynthosError;

/* GPU architecture enumeration */
typedef enum {
    SYNTHOS_ARCH_UNKNOWN = 0,
    SYNTHOS_ARCH_PASCAL = 60,      /* GTX 10-series, minimum baseline */
    SYNTHOS_ARCH_VOLTA = 70,       /* V100 */
    SYNTHOS_ARCH_TURING = 75,      /* RTX 20-series */
    SYNTHOS_ARCH_AMPERE = 80,      /* A100 */
    SYNTHOS_ARCH_AMPERE_RTX = 86,  /* RTX 30-series */
    SYNTHOS_ARCH_ADA = 89,         /* RTX 40-series */
    SYNTHOS_ARCH_HOPPER = 90       /* H100 */
} SynthosArch;

/* Device information structure */
typedef struct {
    int device_id;
    int compute_capability_major;
    int compute_capability_minor;
    SynthosArch arch;
    const char* arch_name;
    size_t global_memory_bytes;
    int multiprocessor_count;
    int max_threads_per_block;
    int warp_size;
    size_t shared_memory_per_block;
    int max_grid_dim_x;
    float expected_speedup_vs_pytorch;  /* Estimated speedup factor */
} SynthosDeviceInfo;

/* Workspace size requirements */
typedef struct {
    size_t fft_workspace_bytes;      /* cuFFT workspace */
    size_t reduction_workspace_bytes; /* Intermediate reduction storage */
    size_t total_bytes;              /* Total required workspace */
} SynthosWorkspaceInfo;

/**
 * @brief Initialize the SynthOS kernel library
 * 
 * Detects GPU architecture, selects optimal kernel variant,
 * and creates cuFFT plans. Must be called before any computation.
 * 
 * @param device_id CUDA device ID to use (usually 0)
 * @return SYNTHOS_SUCCESS on success, error code otherwise
 */
cudaError_t synthos_init(int device_id);

/**
 * @brief Main fused spectral entropy computation kernel
 * 
 * Computes per-channel spectral entropy from input signal matrix:
 *   1. Batch R2C FFT transform
 *   2. Power spectral density (|FFT|²)
 *   3. L1 normalization per channel
 *   4. Spectral entropy: -sum(p * log(p))
 * 
 * All operations are fused into minimal kernel launches with
 * architecture-specific optimizations automatically applied.
 * 
 * @param d_input     Device pointer to input signal [n_samples × n_channels]
 *                    Must be contiguous float32, column-major preferred
 * @param d_entropy   Device pointer to output entropy [n_channels]
 * @param n_samples   Number of samples per channel (FFT size, must be power of 2)
 * @param n_channels  Number of independent channels (batch size)
 * @param stream      CUDA stream for async execution (can be NULL for default)
 * @return SYNTHOS_SUCCESS on success, error code otherwise
 */
cudaError_t synthos_fused_spectral_entropy(
    const float* d_input,
    float* d_entropy,
    int n_samples,
    int n_channels,
    cudaStream_t stream
);

/**
 * @brief Extended API with workspace management
 * 
 * Allows caller to manage workspace memory for reduced allocations
 * in repeated calls. Use synthos_get_workspace_size() first.
 * 
 * @param d_input     Device pointer to input signal [n_samples × n_channels]
 * @param d_entropy   Device pointer to output entropy [n_channels]
 * @param d_workspace Device pointer to workspace memory
 * @param workspace_bytes Size of provided workspace in bytes
 * @param n_samples   Number of samples per channel
 * @param n_channels  Number of channels
 * @param stream      CUDA stream
 * @return SYNTHOS_SUCCESS on success, error code otherwise
 */
cudaError_t synthos_fused_spectral_entropy_ex(
    const float* d_input,
    float* d_entropy,
    void* d_workspace,
    size_t workspace_bytes,
    int n_samples,
    int n_channels,
    cudaStream_t stream
);

/**
 * @brief Get workspace memory requirements
 * 
 * @param n_samples   FFT size
 * @param n_channels  Batch size
 * @param info        Output workspace info structure
 * @return SYNTHOS_SUCCESS on success
 */
cudaError_t synthos_get_workspace_size(
    int n_samples,
    int n_channels,
    SynthosWorkspaceInfo* info
);

/**
 * @brief Get currently active GPU architecture name
 * 
 * Returns human-readable string like "sm_80 Ampere A100"
 * 
 * @return Architecture name string (static, do not free)
 */
const char* synthos_get_active_arch(void);

/**
 * @brief Get detailed device information
 * 
 * @param info Output device info structure
 * @return SYNTHOS_SUCCESS on success
 */
cudaError_t synthos_get_device_info(SynthosDeviceInfo* info);

/**
 * @brief Get error string for SynthOS error code
 * 
 * @param error Error code
 * @return Error description string (static, do not free)
 */
const char* synthos_get_error_string(SynthosError error);

/**
 * @brief Check if GPU is supported
 * 
 * @param device_id CUDA device ID
 * @return 1 if supported, 0 if not
 */
int synthos_is_device_supported(int device_id);

/**
 * @brief Cleanup resources and release cuFFT plans
 * 
 * @return SYNTHOS_SUCCESS on success
 */
cudaError_t synthos_cleanup(void);

/**
 * @brief Synchronize all pending operations
 * 
 * Useful for profiling to ensure all kernels have completed.
 * 
 * @return SYNTHOS_SUCCESS on success
 */
cudaError_t synthos_synchronize(void);

/**
 * @brief Get version string
 * 
 * @return Version string like "1.0.0 (built for sm_60,sm_70,sm_75,sm_80,sm_86,sm_89,sm_90)"
 */
const char* synthos_get_version(void);

#ifdef __cplusplus
}
#endif

#endif /* SYNTHOS_SPECTRAL_H */
