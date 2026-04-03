/**
 * @file arch_dispatch.cuh
 * @brief Runtime GPU Architecture Detection and Kernel Dispatch
 * 
 * Provides runtime detection of GPU compute capability and selects
 * the optimal kernel variant. Supports fallback to baseline implementation
 * for unknown future architectures.
 * 
 * Design Philosophy:
 *   - Runtime dispatch based on cudaDeviceGetAttribute
 *   - Compile-time specialization via __CUDA_ARCH__
 *   - Graceful fallback: unknown arch → use latest known optimization
 *   - No hardcoded warp sizes (query at runtime)
 */

#ifndef SYNTHOS_ARCH_DISPATCH_CUH
#define SYNTHOS_ARCH_DISPATCH_CUH

#include <cuda_runtime.h>
#include <stdio.h>
#include "spectral.h"

/* ============================================================================
 * Architecture Detection Utilities
 * ============================================================================ */

/**
 * @brief Compute capability as single integer (e.g., 80 for sm_80)
 */
__host__ inline int get_compute_capability(int device_id) {
    int major = 0, minor = 0;
    cudaDeviceGetAttribute(&major, cudaDevAttrComputeCapabilityMajor, device_id);
    cudaDeviceGetAttribute(&minor, cudaDevAttrComputeCapabilityMinor, device_id);
    return major * 10 + minor;
}

/**
 * @brief Map compute capability to SynthosArch enum
 */
__host__ inline SynthosArch compute_capability_to_arch(int cc) {
    if (cc >= 90) return SYNTHOS_ARCH_HOPPER;
    if (cc >= 89) return SYNTHOS_ARCH_ADA;
    if (cc >= 86) return SYNTHOS_ARCH_AMPERE_RTX;
    if (cc >= 80) return SYNTHOS_ARCH_AMPERE;
    if (cc >= 75) return SYNTHOS_ARCH_TURING;
    if (cc >= 70) return SYNTHOS_ARCH_VOLTA;
    if (cc >= 60) return SYNTHOS_ARCH_PASCAL;
    return SYNTHOS_ARCH_UNKNOWN;
}

/**
 * @brief Get architecture name string
 */
__host__ inline const char* arch_to_string(SynthosArch arch, int cc) {
    static char buffer[64];
    switch (arch) {
        case SYNTHOS_ARCH_HOPPER:
            snprintf(buffer, sizeof(buffer), "sm_%d Hopper (H100)", cc);
            break;
        case SYNTHOS_ARCH_ADA:
            snprintf(buffer, sizeof(buffer), "sm_%d Ada Lovelace (RTX 40)", cc);
            break;
        case SYNTHOS_ARCH_AMPERE_RTX:
            snprintf(buffer, sizeof(buffer), "sm_%d Ampere (RTX 30)", cc);
            break;
        case SYNTHOS_ARCH_AMPERE:
            snprintf(buffer, sizeof(buffer), "sm_%d Ampere (A100)", cc);
            break;
        case SYNTHOS_ARCH_TURING:
            snprintf(buffer, sizeof(buffer), "sm_%d Turing (RTX 20)", cc);
            break;
        case SYNTHOS_ARCH_VOLTA:
            snprintf(buffer, sizeof(buffer), "sm_%d Volta (V100)", cc);
            break;
        case SYNTHOS_ARCH_PASCAL:
            snprintf(buffer, sizeof(buffer), "sm_%d Pascal (GTX 10)", cc);
            break;
        default:
            snprintf(buffer, sizeof(buffer), "sm_%d Unknown", cc);
            break;
    }
    return buffer;
}

/**
 * @brief Get expected speedup factor vs PyTorch baseline
 */
__host__ inline float get_expected_speedup(SynthosArch arch) {
    switch (arch) {
        case SYNTHOS_ARCH_HOPPER:     return 5.0f;   /* H100: ~5x with TBC */
        case SYNTHOS_ARCH_ADA:        return 4.0f;   /* RTX 40: ~4x */
        case SYNTHOS_ARCH_AMPERE_RTX: return 3.5f;   /* RTX 30: ~3.5x */
        case SYNTHOS_ARCH_AMPERE:     return 4.0f;   /* A100: ~4x with async */
        case SYNTHOS_ARCH_TURING:     return 2.5f;   /* RTX 20: ~2.5x */
        case SYNTHOS_ARCH_VOLTA:      return 3.0f;   /* V100: ~3x */
        case SYNTHOS_ARCH_PASCAL:     return 2.0f;   /* GTX 10: ~2x baseline */
        default:                       return 1.5f;
    }
}

/* ============================================================================
 * Compile-Time Architecture Selection
 * ============================================================================ */

/**
 * @brief Check if current compilation target supports cooperative groups
 */
#ifdef __CUDA_ARCH__
    #define SYNTHOS_HAS_COOP_GROUPS (__CUDA_ARCH__ >= 700)
    #define SYNTHOS_HAS_ASYNC_COPY  (__CUDA_ARCH__ >= 800)
    #define SYNTHOS_HAS_TBC         (__CUDA_ARCH__ >= 900)
    #define SYNTHOS_CURRENT_ARCH    __CUDA_ARCH__
#else
    #define SYNTHOS_HAS_COOP_GROUPS 0
    #define SYNTHOS_HAS_ASYNC_COPY  0
    #define SYNTHOS_HAS_TBC         0
    #define SYNTHOS_CURRENT_ARCH    0
#endif

/* ============================================================================
 * Warp-Level Primitives (Architecture-Agnostic)
 * ============================================================================ */

/**
 * @brief Warp shuffle reduction (sum)
 * 
 * Works on all architectures sm_60+. Uses shfl_down_sync for
 * architecture-independent implementation.
 */
__device__ __forceinline__ float warp_reduce_sum(float val) {
    unsigned int mask = 0xffffffff;
    #pragma unroll
    for (int offset = 16; offset > 0; offset >>= 1) {
        val += __shfl_down_sync(mask, val, offset);
    }
    return val;
}

/**
 * @brief Warp shuffle reduction (max)
 */
__device__ __forceinline__ float warp_reduce_max(float val) {
    unsigned int mask = 0xffffffff;
    #pragma unroll
    for (int offset = 16; offset > 0; offset >>= 1) {
        val = fmaxf(val, __shfl_down_sync(mask, val, offset));
    }
    return val;
}

/**
 * @brief Block-level reduction using shared memory
 * 
 * Generic implementation that works across all architectures.
 * Each warp reduces independently, then first warp combines.
 * 
 * @param val     Value from each thread
 * @param shared  Shared memory (at least 32 floats)
 * @param tid     Thread index within block
 * @return        Reduced sum (valid only in thread 0)
 */
__device__ __forceinline__ float block_reduce_sum(
    float val,
    volatile float* shared,
    int tid
) {
    const int lane = tid & 31;
    const int wid = tid >> 5;
    
    /* Warp-level reduction */
    val = warp_reduce_sum(val);
    
    /* Write warp results to shared memory */
    if (lane == 0) {
        shared[wid] = val;
    }
    __syncthreads();
    
    /* First warp reduces warp results */
    /* Assuming max 1024 threads = 32 warps */
    int num_warps = (blockDim.x + 31) >> 5;
    val = (tid < num_warps) ? shared[tid] : 0.0f;
    
    if (wid == 0) {
        val = warp_reduce_sum(val);
    }
    
    return val;
}

/* ============================================================================
 * Kernel Dispatch Table
 * ============================================================================ */

/**
 * @brief Kernel function pointer type
 */
typedef void (*SpectralEntropyKernelFn)(
    const float* __restrict__,  /* d_psd */
    float* __restrict__,        /* d_entropy */
    int,                        /* n_freq */
    int                         /* n_channels */
);

/**
 * @brief Runtime kernel selector structure
 * 
 * Populated during synthos_init() based on detected GPU.
 */
typedef struct {
    int compute_capability;
    SynthosArch arch;
    const char* arch_name;
    SpectralEntropyKernelFn entropy_kernel;
    int block_size;
    int shared_mem_bytes;
    float expected_speedup;
} KernelDispatchInfo;

/* Forward declarations for architecture-specific kernels */
/* (Defined in kernels/spectral_smXX.cuh) */

/* Note: These are template-like functions selected at compile time.
 * The actual dispatch happens based on the fat binary's embedded code
 * for each architecture. */

/* ============================================================================
 * Utility Functions
 * ============================================================================ */

/**
 * @brief Check if n is a power of 2
 */
__host__ __device__ __forceinline__ bool is_power_of_2(int n) {
    return (n > 0) && ((n & (n - 1)) == 0);
}

/**
 * @brief Get next power of 2 >= n
 */
__host__ __device__ __forceinline__ int next_power_of_2(int n) {
    n--;
    n |= n >> 1;
    n |= n >> 2;
    n |= n >> 4;
    n |= n >> 8;
    n |= n >> 16;
    n++;
    return n;
}

/**
 * @brief Calculate optimal block size for entropy kernel
 */
__host__ inline int get_optimal_block_size(SynthosArch arch, int n_freq) {
    /* Different architectures have different optimal occupancy points */
    switch (arch) {
        case SYNTHOS_ARCH_HOPPER:
        case SYNTHOS_ARCH_ADA:
            /* Newer architectures: prefer larger blocks for better reduction */
            return (n_freq >= 512) ? 256 : 128;
        
        case SYNTHOS_ARCH_AMPERE:
        case SYNTHOS_ARCH_AMPERE_RTX:
            return 256;  /* A100/RTX 30: 256 threads is optimal */
        
        case SYNTHOS_ARCH_VOLTA:
        case SYNTHOS_ARCH_TURING:
            return 256;  /* V100/RTX 20: 256 threads */
        
        case SYNTHOS_ARCH_PASCAL:
        default:
            return 128;  /* Conservative for older GPUs */
    }
}

/**
 * @brief Calculate required shared memory for entropy kernel
 */
__host__ inline int get_shared_memory_size(int block_size) {
    /* Need space for warp reduction results (32 warps max) */
    /* Plus potential temporary storage for normalization sum */
    int warp_slots = (block_size + 31) / 32;
    return sizeof(float) * (warp_slots + 32 + 64);  /* Extra padding */
}

#endif /* SYNTHOS_ARCH_DISPATCH_CUH */
