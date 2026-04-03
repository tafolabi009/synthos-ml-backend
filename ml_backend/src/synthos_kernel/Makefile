# ==============================================================================
# SynthOS Spectral Entropy Kernel - Makefile
# ==============================================================================
#
# Alternative build system for environments without CMake.
#
# Usage:
#   make                    # Build with all architectures
#   make CUDA_ARCH=native   # Build for detected GPU only
#   make CUDA_ARCH=80       # Build for specific architecture
#   make clean              # Clean build artifacts
#   make install            # Install to system/Python
#   make test               # Build and run tests
#
# ==============================================================================

# Project settings
PROJECT := synthos
VERSION := 1.0.0
LIB_NAME := lib$(PROJECT).so
STATIC_LIB := lib$(PROJECT)_static.a

# Directories
SRC_DIR := .
BUILD_DIR := build
KERNELS_DIR := kernels

# CUDA configuration
CUDA_PATH ?= /usr/local/cuda
NVCC := $(CUDA_PATH)/bin/nvcc
CUDA_INCLUDE := $(CUDA_PATH)/include
CUDA_LIB := $(CUDA_PATH)/lib64

# Default architecture: all supported
CUDA_ARCH ?= all

# Architecture definitions
ARCH_60 := -gencode arch=compute_60,code=sm_60
ARCH_70 := -gencode arch=compute_70,code=sm_70
ARCH_75 := -gencode arch=compute_75,code=sm_75
ARCH_80 := -gencode arch=compute_80,code=sm_80
ARCH_86 := -gencode arch=compute_86,code=sm_86
ARCH_89 := -gencode arch=compute_89,code=sm_89
ARCH_90 := -gencode arch=compute_90,code=sm_90
ARCH_PTX := -gencode arch=compute_90,code=compute_90

# Select architecture flags based on CUDA_ARCH
ifeq ($(CUDA_ARCH),all)
    ARCH_FLAGS := $(ARCH_60) $(ARCH_70) $(ARCH_75) $(ARCH_80) $(ARCH_86) $(ARCH_89) $(ARCH_90) $(ARCH_PTX)
    ARCH_DESC := "sm_60,sm_70,sm_75,sm_80,sm_86,sm_89,sm_90+PTX"
else ifeq ($(CUDA_ARCH),native)
    # Auto-detect GPU
    GPU_ARCH := $(shell $(NVCC) --list-gpu-arch 2>/dev/null | head -1 | sed 's/compute_//')
    ifndef GPU_ARCH
        GPU_ARCH := 70
    endif
    ARCH_FLAGS := -gencode arch=compute_$(GPU_ARCH),code=sm_$(GPU_ARCH) $(ARCH_PTX)
    ARCH_DESC := "sm_$(GPU_ARCH)+PTX (native)"
else ifeq ($(CUDA_ARCH),datacenter)
    # V100, A100, H100 only
    ARCH_FLAGS := $(ARCH_70) $(ARCH_80) $(ARCH_90) $(ARCH_PTX)
    ARCH_DESC := "sm_70,sm_80,sm_90+PTX (datacenter)"
else
    # Specific architecture(s)
    ARCH_FLAGS := -gencode arch=compute_$(CUDA_ARCH),code=sm_$(CUDA_ARCH) $(ARCH_PTX)
    ARCH_DESC := "sm_$(CUDA_ARCH)+PTX"
endif

# Compiler flags
NVCC_FLAGS := $(ARCH_FLAGS)
NVCC_FLAGS += -std=c++17
NVCC_FLAGS += -Xcompiler -fPIC
NVCC_FLAGS += -Xcompiler -Wall
NVCC_FLAGS += --expt-extended-lambda
NVCC_FLAGS += --expt-relaxed-constexpr
NVCC_FLAGS += -I$(CUDA_INCLUDE)
NVCC_FLAGS += -I$(SRC_DIR)

# Release flags (default)
NVCC_FLAGS += -O3 -DNDEBUG -use_fast_math

# Debug build
ifdef DEBUG
    NVCC_FLAGS := $(filter-out -O3 -DNDEBUG -use_fast_math,$(NVCC_FLAGS))
    NVCC_FLAGS += -G -g -O0 -DDEBUG
endif

# Profiling
ifdef PROFILE
    NVCC_FLAGS += -lineinfo
endif

# Linker flags
LDFLAGS := -L$(CUDA_LIB)
LDFLAGS += -lcudart
LDFLAGS += -lcufft
LDFLAGS += -shared

# Source files
SOURCES := spectral.cu
HEADERS := spectral.h arch_dispatch.cuh
KERNEL_HEADERS := $(wildcard $(KERNELS_DIR)/*.cuh)

# Objects
OBJECTS := $(BUILD_DIR)/spectral.o

# ==============================================================================
# Targets
# ==============================================================================

.PHONY: all clean install test info help

all: info $(BUILD_DIR)/$(LIB_NAME)
	@echo ""
	@echo "Build complete: $(BUILD_DIR)/$(LIB_NAME)"
	@echo "Architectures: $(ARCH_DESC)"
	@ls -lh $(BUILD_DIR)/$(LIB_NAME)

$(BUILD_DIR):
	@mkdir -p $(BUILD_DIR)

$(BUILD_DIR)/%.o: %.cu $(HEADERS) $(KERNEL_HEADERS) | $(BUILD_DIR)
	@echo "Compiling $<..."
	$(NVCC) $(NVCC_FLAGS) -c $< -o $@

$(BUILD_DIR)/$(LIB_NAME): $(OBJECTS)
	@echo "Linking $(LIB_NAME)..."
	$(NVCC) $(OBJECTS) $(LDFLAGS) -o $@
	@echo ""
	@echo "=== Binary Size Report ==="
	@ls -lh $@
	@size $@ 2>/dev/null || true

# Static library
$(BUILD_DIR)/$(STATIC_LIB): $(OBJECTS)
	@echo "Creating static library..."
	ar rcs $@ $(OBJECTS)

static: $(BUILD_DIR)/$(STATIC_LIB)

# ==============================================================================
# Installation
# ==============================================================================

PREFIX ?= /usr/local
PYTHON_SITE_PACKAGES := $(shell python3 -c "import sysconfig; print(sysconfig.get_paths()['purelib'])" 2>/dev/null)

install: $(BUILD_DIR)/$(LIB_NAME)
	@echo "Installing to $(PREFIX)..."
	install -d $(PREFIX)/lib
	install -d $(PREFIX)/include/synthos
	install -m 755 $(BUILD_DIR)/$(LIB_NAME) $(PREFIX)/lib/
	install -m 644 spectral.h $(PREFIX)/include/synthos/
	ldconfig 2>/dev/null || true
	@echo "Installed to $(PREFIX)"

install-python: $(BUILD_DIR)/$(LIB_NAME)
ifdef PYTHON_SITE_PACKAGES
	@echo "Installing to Python: $(PYTHON_SITE_PACKAGES)/synthos_kernel/"
	install -d $(PYTHON_SITE_PACKAGES)/synthos_kernel
	install -m 755 $(BUILD_DIR)/$(LIB_NAME) $(PYTHON_SITE_PACKAGES)/synthos_kernel/
	install -m 644 python_wrapper.py $(PYTHON_SITE_PACKAGES)/synthos_kernel/__init__.py
	@echo "Python package installed successfully"
else
	@echo "Error: Python not found or site-packages not accessible"
	@exit 1
endif

# ==============================================================================
# Development
# ==============================================================================

# Build for testing/development (native GPU only)
dev:
	$(MAKE) CUDA_ARCH=native DEBUG=1

# Build with profiling annotations
profile:
	$(MAKE) PROFILE=1

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf $(BUILD_DIR)
	rm -f *.o *.so *.a

# ==============================================================================
# Testing
# ==============================================================================

test: $(BUILD_DIR)/$(LIB_NAME)
	@echo "Running Python tests..."
	@cd $(SRC_DIR) && python3 -c "\
import sys; \
sys.path.insert(0, '.'); \
from python_wrapper import SynthOSKernel; \
k = SynthOSKernel('$(BUILD_DIR)/$(LIB_NAME)'); \
print('Library loaded successfully'); \
print(f'Active architecture: {k.active_arch}'); \
print(f'GPU info: {k.get_performance_info()}'); \
" 2>&1 && echo "Basic test PASSED" || echo "Basic test FAILED"

benchmark: $(BUILD_DIR)/$(LIB_NAME)
	@echo "Running benchmark..."
	python3 test_spectral.py --lib $(BUILD_DIR)/$(LIB_NAME)

# ==============================================================================
# Information
# ==============================================================================

info:
	@echo "=============== SynthOS Kernel Build ==============="
	@echo "  Version:      $(VERSION)"
	@echo "  CUDA Path:    $(CUDA_PATH)"
	@echo "  NVCC:         $(shell $(NVCC) --version | head -1)"
	@echo "  Architectures: $(ARCH_DESC)"
	@echo "  Build type:   $(if $(DEBUG),Debug,Release)"
	@echo "====================================================="

help:
	@echo "SynthOS Kernel Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  all             Build shared library (default)"
	@echo "  static          Build static library"
	@echo "  clean           Remove build artifacts"
	@echo "  install         Install to system (PREFIX=/usr/local)"
	@echo "  install-python  Install to Python site-packages"
	@echo "  test            Run basic functionality test"
	@echo "  benchmark       Run performance benchmark"
	@echo "  dev             Build for development (native GPU, debug)"
	@echo "  profile         Build with profiling annotations"
	@echo "  info            Show build configuration"
	@echo "  help            Show this help"
	@echo ""
	@echo "Variables:"
	@echo "  CUDA_ARCH=all|native|datacenter|60|70|80|90|..."
	@echo "  CUDA_PATH=/path/to/cuda"
	@echo "  DEBUG=1         Enable debug build"
	@echo "  PROFILE=1       Enable profiling"
	@echo "  PREFIX=/path    Installation prefix"
	@echo ""
	@echo "Examples:"
	@echo "  make                          # Full fat binary"
	@echo "  make CUDA_ARCH=native         # Native GPU only"
	@echo "  make CUDA_ARCH=80 DEBUG=1     # A100, debug mode"
	@echo "  make install-python           # Install Python package"
