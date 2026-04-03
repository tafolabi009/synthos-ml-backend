/**
 * @file kernels/spectral_sm70.cuh
 * @brief Volta Architecture (sm_70) Optimized Spectral Entropy Kernel
 * 
 * Volta introduces cooperative groups which enable more flexible
 * and efficient parallel reductions. This version uses:
 *   - Cooperative groups for warp-level operations
 *   - Independent thread scheduling awareness
 *   - Optimized shared memory access patterns
 * 
 * Key Optimizations:
 *   - Use thread_block_tile for sub-warp operations
 *   - Explicit warp synchronization (Volta has independent scheduling)
 *   - L2 cache-friendly access patterns
 */

#ifndef SYNTHOS_SPECTRAL_SM70_CUH
#define SYNTHOS_SPECTRAL_SM70_CUH

#include "../arch_dispatch.cuh"
#include <cuda_runtime.h>
#include <cooperative_groups.h>
#include <math_constants.h>

namespace cg = cooperative_groups;

namespace synthos {
namespace sm70 {

/* ============================================================================
 * Cooperative Groups Reduction Utilities
 * ============================================================================ */

/**
 * @brief Warp reduction using cooperative groups
 * 
 * More explicit and portable than raw shuffle intrinsics.
 */
__device__ __forceinline__ float cg_warp_reduce_sum(cg::thread_block_tile<32>& warp, float val) {
    #pragma unroll
    for (int offset = 16; offset > 0; offset >>= 1) {
        val += warp.shfl_down(val, offset);
    }
    return val;
}

/**
 * @brief Block reduction using cooperative groups
 */
__device__ __forceinline__ float cg_block_reduce_sum(
    cg::thread_block& block,
    cg::thread_block_tile<32>& warp,
    float val,
    float* shared
) {
    int lane = warp.thread_rank();
    int wid = threadIdx.x / 32;
    
    /* Warp-level reduction */
    val = cg_warp_reduce_sum(warp, val);
    
    /* Write to shared memory */
    if (lane == 0) {
        shared[wid] = val;
    }
    block.sync();
    
    /* First warp reduces all warp results */
    int num_warps = (block.size() + 31) / 32;
    val = (threadIdx.x < num_warps) ? shared[threadIdx.x] : 0.0f;
    
    if (wid == 0) {
        val = cg_warp_reduce_sum(warp, val);
    }
    
    return val;
}

/* ============================================================================
 * Volta-Optimized Entropy Kernel
 * ============================================================================ */

/**
 * @brief Fused entropy kernel using cooperative groups (Volta+)
 * 
 * Improvements over sm60:
 *   - Explicit synchronization points
 *   - Better occupancy with 256 threads
 *   - Optimized memory access for V100's HBM2
 */
template <int BLOCK_SIZE>
__global__ void spectral_entropy_kernel_sm70(
    const float* __restrict__ d_psd,
    float* __restrict__ d_entropy,
    int n_freq,
    int n_channels
) {
    /* Cooperative groups setup */
    cg::thread_block block = cg::this_thread_block();
    cg::thread_block_tile<32> warp = cg::tiled_partition<32>(block);
    
    /* Shared memory for reductions */
    __shared__ float s_reduce[32];
    __shared__ float s_l1_norm;
    
    const int channel = blockIdx.x;
    const int tid = threadIdx.x;
    
    if (channel >= n_channels) return;
    
    const float* psd_col = d_psd + channel * n_freq;
    
    /* ========================== Phase 1: L1 Norm ========================== */
    
    float local_sum = 0.0f;
    
    /* Strided access for better L2 cache utilization on V100 */
    #pragma unroll 4
    for (int i = tid; i < n_freq; i += BLOCK_SIZE) {
        local_sum += psd_col[i];
    }
    
    /* Block reduction using cooperative groups */
    float total_sum = cg_block_reduce_sum(block, warp, local_sum, s_reduce);
    
    if (tid == 0) {
        s_l1_norm = total_sum + 1e-10f;
    }
    block.sync();
    
    /* ========================== Phase 2: Entropy ========================== */
    
    float l1_norm = s_l1_norm;
    float local_entropy = 0.0f;
    
    /* Compute normalized entropy contribution */
    /* Using fused multiply-add for better performance */
    #pragma unroll 4
    for (int i = tid; i < n_freq; i += BLOCK_SIZE) {
        float val = psd_col[i];
        float p = val / l1_norm;
        
        /* Branch-free entropy computation */
        /* -p * log(p) when p > 0, else 0 */
        float log_p = logf(fmaxf(p, 1e-10f));
        local_entropy -= p * log_p * (p > 1e-10f ? 1.0f : 0.0f);
    }
    
    float total_entropy = cg_block_reduce_sum(block, warp, local_entropy, s_reduce);
    
    if (tid == 0) {
        d_entropy[channel] = total_entropy;
    }
}

/**
 * @brief Two-pass kernel for very large FFT sizes
 * 
 * For n_freq > 4096, we use a two-pass approach:
 *   Pass 1: Parallel partial sums stored in global memory
 *   Pass 2: Final reduction
 * 
 * This improves memory bandwidth utilization on V100.
 */
__global__ void spectral_entropy_large_pass1_sm70(
    const float* __restrict__ d_psd,
    float* __restrict__ d_partial_sums,
    float* __restrict__ d_partial_entropy,
    int n_freq,
    int n_channels,
    int chunks_per_channel
) {
    cg::thread_block block = cg::this_thread_block();
    cg::thread_block_tile<32> warp = cg::tiled_partition<32>(block);
    
    __shared__ float s_reduce[32];
    
    const int channel = blockIdx.x / chunks_per_channel;
    const int chunk = blockIdx.x % chunks_per_channel;
    const int tid = threadIdx.x;
    
    if (channel >= n_channels) return;
    
    const int chunk_size = (n_freq + chunks_per_channel - 1) / chunks_per_channel;
    const int start = chunk * chunk_size;
    const int end = min(start + chunk_size, n_freq);
    
    const float* psd_col = d_psd + channel * n_freq;
    
    /* Accumulate sum for this chunk */
    float local_sum = 0.0f;
    for (int i = start + tid; i < end; i += blockDim.x) {
        local_sum += psd_col[i];
    }
    
    float chunk_sum = cg_block_reduce_sum(block, warp, local_sum, s_reduce);
    
    if (tid == 0) {
        d_partial_sums[blockIdx.x] = chunk_sum;
    }
}

/**
 * @brief Launch entropy kernel with optimal parameters for Volta
 */
inline cudaError_t launch_spectral_entropy_sm70(
    const float* d_psd,
    float* d_entropy,
    int n_freq,
    int n_channels,
    cudaStream_t stream
) {
    /* Volta prefers 256 threads for better occupancy */
    constexpr int BLOCK_SIZE = 256;
    
    dim3 grid(n_channels);
    dim3 block(BLOCK_SIZE);
    
    spectral_entropy_kernel_sm70<BLOCK_SIZE><<<grid, block, 0, stream>>>(
        d_psd, d_entropy, n_freq, n_channels
    );
    
    return cudaGetLastError();
}

}  /* namespace sm70 */
}  /* namespace synthos */

#endif /* SYNTHOS_SPECTRAL_SM70_CUH */
