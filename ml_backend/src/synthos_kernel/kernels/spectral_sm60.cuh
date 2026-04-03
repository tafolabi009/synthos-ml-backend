/**
 * @file kernels/spectral_sm60.cuh
 * @brief Pascal Architecture (sm_60/61) Baseline Spectral Entropy Kernel
 * 
 * This is the baseline implementation that works on all sm_60+ GPUs.
 * Uses only features available since Pascal:
 *   - Warp shuffle intrinsics (__shfl_down_sync)
 *   - Shared memory for block-level reductions
 *   - Standard memory access patterns
 * 
 * Optimization Strategy:
 *   - Coalesced global memory reads
 *   - Warp-level parallel reductions
 *   - Shared memory for inter-warp communication
 *   - Loop unrolling for common FFT sizes
 */

#ifndef SYNTHOS_SPECTRAL_SM60_CUH
#define SYNTHOS_SPECTRAL_SM60_CUH

#include "../arch_dispatch.cuh"
#include <cuda_runtime.h>
#include <math_constants.h>

namespace synthos {
namespace sm60 {

/* ============================================================================
 * L1 Normalization + Spectral Entropy Fused Kernel (Pascal Baseline)
 * ============================================================================ */

/**
 * @brief Fused L1 normalization and entropy computation
 * 
 * Input: Power spectral density (already computed from FFT)
 * Output: Per-channel spectral entropy
 * 
 * Algorithm:
 *   1. Parallel sum reduction for L1 norm
 *   2. Normalize and compute p * log(p) in one pass
 *   3. Parallel sum reduction for entropy
 * 
 * Memory Pattern:
 *   - Each block processes one channel (column)
 *   - Threads cooperatively load rows (frequency bins)
 *   - Two-phase reduction: warp → block
 * 
 * @param d_psd        Input PSD [n_freq × n_channels], contiguous
 * @param d_entropy    Output entropy [n_channels]
 * @param n_freq       Number of frequency bins (rfft output size)
 * @param n_channels   Number of channels (batch size)
 */
template <int BLOCK_SIZE>
__global__ void spectral_entropy_kernel_sm60(
    const float* __restrict__ d_psd,
    float* __restrict__ d_entropy,
    int n_freq,
    int n_channels
) {
    /* Shared memory for reductions */
    __shared__ float s_sum[32];      /* Warp results for L1 norm */
    __shared__ float s_entropy[32];  /* Warp results for entropy */
    __shared__ float s_l1_norm;      /* Final L1 norm for this channel */
    
    const int channel = blockIdx.x;
    const int tid = threadIdx.x;
    
    if (channel >= n_channels) return;
    
    /* Pointer to this channel's PSD data */
    const float* psd_col = d_psd + channel * n_freq;
    
    /* ========================== Phase 1: L1 Norm ========================== */
    
    /* Each thread accumulates sum of its assigned frequency bins */
    float local_sum = 0.0f;
    
    /* Grid-stride loop for n_freq > BLOCK_SIZE */
    for (int i = tid; i < n_freq; i += BLOCK_SIZE) {
        float val = psd_col[i];
        local_sum += val;
    }
    
    /* Block-level reduction */
    float total_sum = block_reduce_sum(local_sum, s_sum, tid);
    
    /* Thread 0 stores the L1 norm */
    if (tid == 0) {
        s_l1_norm = total_sum + 1e-10f;  /* Epsilon to avoid division by zero */
    }
    __syncthreads();
    
    /* ========================== Phase 2: Entropy ========================== */
    
    float l1_norm = s_l1_norm;
    float local_entropy = 0.0f;
    
    /* Grid-stride loop: normalize and compute entropy contribution */
    for (int i = tid; i < n_freq; i += BLOCK_SIZE) {
        float val = psd_col[i];
        float p = val / l1_norm;  /* Normalized probability */
        
        /* Entropy contribution: -p * log(p) */
        /* Using log1p for numerical stability when p is very small */
        if (p > 1e-10f) {
            local_entropy -= p * logf(p);
        }
    }
    
    /* Block-level reduction for entropy */
    float total_entropy = block_reduce_sum(local_entropy, s_entropy, tid);
    
    /* Thread 0 writes final entropy */
    if (tid == 0) {
        d_entropy[channel] = total_entropy;
    }
}

/**
 * @brief Optimized version for small FFT sizes (n_freq <= 256)
 * 
 * When n_freq fits within a single warp or small block,
 * we can avoid shared memory for the first reduction.
 */
template <int N_FREQ>
__global__ void spectral_entropy_small_sm60(
    const float* __restrict__ d_psd,
    float* __restrict__ d_entropy,
    int n_channels
) {
    static_assert(N_FREQ <= 256, "Use general kernel for large FFT sizes");
    
    __shared__ float s_data[N_FREQ + 32];  /* Combined storage */
    
    const int channel = blockIdx.x;
    const int tid = threadIdx.x;
    
    if (channel >= n_channels) return;
    
    const float* psd_col = d_psd + channel * N_FREQ;
    
    /* Load PSD into shared memory (coalesced) */
    float val = (tid < N_FREQ) ? psd_col[tid] : 0.0f;
    s_data[tid] = val;
    __syncthreads();
    
    /* Parallel reduction for L1 norm */
    /* Using warp shuffle for efficiency */
    float sum = val;
    sum = warp_reduce_sum(sum);
    
    if ((tid & 31) == 0) {
        s_data[N_FREQ + (tid >> 5)] = sum;
    }
    __syncthreads();
    
    /* First warp combines warp sums */
    if (tid < 8) {  /* Max 8 warps for 256 threads */
        sum = s_data[N_FREQ + tid];
        sum = warp_reduce_sum(sum);
        if (tid == 0) {
            s_data[N_FREQ + 31] = sum + 1e-10f;  /* Store L1 norm */
        }
    }
    __syncthreads();
    
    /* Compute entropy */
    float l1_norm = s_data[N_FREQ + 31];
    float entropy_contrib = 0.0f;
    
    if (tid < N_FREQ) {
        float p = val / l1_norm;
        if (p > 1e-10f) {
            entropy_contrib = -p * logf(p);
        }
    }
    
    /* Reduce entropy contributions */
    float entropy = warp_reduce_sum(entropy_contrib);
    
    if ((tid & 31) == 0) {
        s_data[tid >> 5] = entropy;
    }
    __syncthreads();
    
    if (tid < 8) {
        entropy = s_data[tid];
        entropy = warp_reduce_sum(entropy);
        if (tid == 0) {
            d_entropy[channel] = entropy;
        }
    }
}

/**
 * @brief Launch entropy kernel with optimal parameters for Pascal
 */
inline cudaError_t launch_spectral_entropy_sm60(
    const float* d_psd,
    float* d_entropy,
    int n_freq,
    int n_channels,
    cudaStream_t stream
) {
    /* Select kernel variant based on FFT size */
    if (n_freq <= 64) {
        dim3 grid(n_channels);
        dim3 block(64);
        spectral_entropy_small_sm60<64><<<grid, block, 0, stream>>>(
            d_psd, d_entropy, n_channels
        );
    }
    else if (n_freq <= 128) {
        dim3 grid(n_channels);
        dim3 block(128);
        spectral_entropy_small_sm60<128><<<grid, block, 0, stream>>>(
            d_psd, d_entropy, n_channels
        );
    }
    else if (n_freq <= 256) {
        dim3 grid(n_channels);
        dim3 block(256);
        spectral_entropy_small_sm60<256><<<grid, block, 0, stream>>>(
            d_psd, d_entropy, n_channels
        );
    }
    else {
        /* General kernel for larger FFT sizes */
        dim3 grid(n_channels);
        dim3 block(128);  /* Conservative block size for Pascal */
        size_t shared_mem = 0;  /* Using static shared memory */
        
        spectral_entropy_kernel_sm60<128><<<grid, block, shared_mem, stream>>>(
            d_psd, d_entropy, n_freq, n_channels
        );
    }
    
    return cudaGetLastError();
}

}  /* namespace sm60 */
}  /* namespace synthos */

#endif /* SYNTHOS_SPECTRAL_SM60_CUH */
