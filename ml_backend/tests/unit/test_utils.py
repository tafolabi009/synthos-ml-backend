"""
Unit tests for utility modules: gpu_optimizer, error_handling, and metrics.
"""
import pytest
import numpy as np
import torch
import torch.nn as nn
from unittest.mock import MagicMock, patch, call
import time

from src.utils.gpu_optimizer import GPUOptimizer, OptimizationConfig, GPUMetrics
from src.utils.error_handling import (
    CircuitBreaker, with_retries, RetryableError,
    ResourceExhaustedError, TransientError, ValidationError,
    ErrorCategory
)
from src.utils.metrics import MetricsCollector


class TestGPUOptimizer:
    
    @pytest.fixture
    def optimizer(self):
        """Create GPU optimizer instance with GPU disabled for testing"""
        return GPUOptimizer(use_gpu=False)
    
    def test_optimization_config_defaults(self):
        """Test OptimizationConfig default values"""
        config = OptimizationConfig()
        assert config.use_mixed_precision == True
        assert config.precision == "bf16"
        assert config.gradient_checkpointing == True
        assert config.compile_model == True
        assert config.num_workers == 8
        assert config.pin_memory == True
    
    def test_optimization_config_custom(self):
        """Test OptimizationConfig with custom values"""
        config = OptimizationConfig(
            use_mixed_precision=False,
            precision="fp16",
            num_workers=4,
            pin_memory=False
        )
        assert config.use_mixed_precision == False
        assert config.precision == "fp16"
        assert config.num_workers == 4
        assert config.pin_memory == False
    
    def test_gpu_optimizer_cpu_mode(self, optimizer):
        """Test GPU optimizer in CPU mode"""
        assert optimizer.use_gpu == False
        assert optimizer.scaler is None
    
    @patch('torch.cuda.is_available', return_value=False)
    def test_gpu_unavailable(self, mock_cuda):
        """Test GPU optimizer when CUDA is not available"""
        optimizer = GPUOptimizer(use_gpu=True)  # Try to use GPU
        assert optimizer.use_gpu == False  # Should fall back to CPU
    
    def test_gpu_metrics_dataclass(self):
        """Test GPUMetrics dataclass"""
        metrics = GPUMetrics(
            gpu_id=0,
            utilization_percent=75.0,
            memory_used_gb=8.0,
            memory_total_gb=16.0,
            memory_percent=50.0,
            temperature_c=65.0,
            power_watts=200.0
        )
        assert metrics.gpu_id == 0
        assert metrics.utilization_percent == 75.0
        assert metrics.memory_percent == 50.0


class TestCircuitBreaker:
    
    def test_circuit_breaker_init(self):
        """Test circuit breaker initialization"""
        breaker = CircuitBreaker(failure_threshold=5, timeout_duration=30.0)
        assert breaker.failure_threshold == 5
        assert breaker.timeout_duration == 30.0
        assert breaker.state == "closed"
    
    def test_circuit_breaker_success(self):
        """Test circuit breaker with successful calls"""
        breaker = CircuitBreaker(failure_threshold=3, timeout_duration=1.0)
        
        def successful_func():
            return "success"
        
        # Should succeed
        for _ in range(5):
            result = breaker.call(successful_func)
            assert result == "success"
        
        assert breaker.state == "closed"
        assert breaker.failure_count == 0
    
    def test_circuit_breaker_opens(self):
        """Test circuit breaker opens after threshold"""
        breaker = CircuitBreaker(failure_threshold=3, timeout_duration=0.5)
        
        def failing_func():
            raise Exception("Error")
        
        # First 3 calls should fail normally
        for _ in range(3):
            with pytest.raises(Exception):
                breaker.call(failing_func)
        
        # Circuit should now be open
        assert breaker.state == "open"
        
        # Next call should fail immediately
        with pytest.raises(Exception):
            breaker.call(failing_func)
    
    def test_circuit_breaker_half_open(self):
        """Test circuit breaker half-open state"""
        breaker = CircuitBreaker(failure_threshold=2, timeout_duration=0.1)
        
        call_count = [0]
        
        def sometimes_failing():
            call_count[0] += 1
            if call_count[0] <= 2:
                raise Exception("Error")
            return "success"
        
        # Fail twice to open circuit
        for _ in range(2):
            with pytest.raises(Exception):
                breaker.call(sometimes_failing)
        
        assert breaker.state == "open"
        
        # Wait for timeout
        time.sleep(0.15)
        
        # Next call should work (half-open state allows retry)
        result = breaker.call(sometimes_failing)
        assert result == "success"
        assert breaker.state == "closed"


class TestRetries:
    
    def test_with_retries_decorator_exists(self):
        """Test retry decorator exists and is callable"""
        @with_retries(max_attempts=3)
        def test_func():
            return "success"
        
        # Just verify the decorator works
        assert callable(test_func)
    
    def test_with_retries_decorator_params(self):
        """Test retry decorator with various parameters"""
        @with_retries(max_attempts=5, initial_wait=0.5, max_wait=5.0)
        def test_func():
            return "result"
        
        assert callable(test_func)


class TestErrorTypes:
    
    def test_retryable_error(self):
        """Test RetryableError is an exception"""
        error = RetryableError("Test error")
        assert isinstance(error, Exception)
        assert str(error) == "Test error"
    
    def test_resource_exhausted_error(self):
        """Test ResourceExhaustedError inherits from RetryableError"""
        error = ResourceExhaustedError("Out of memory")
        assert isinstance(error, RetryableError)
        assert isinstance(error, Exception)
    
    def test_transient_error(self):
        """Test TransientError inherits from RetryableError"""
        error = TransientError("Network timeout")
        assert isinstance(error, RetryableError)
    
    def test_validation_error(self):
        """Test ValidationError is not retryable"""
        error = ValidationError("Invalid input")
        assert isinstance(error, Exception)
        assert not isinstance(error, RetryableError)


class TestErrorCategory:
    
    def test_error_categories(self):
        """Test error category enum values"""
        assert ErrorCategory.TRANSIENT.value == "transient"
        assert ErrorCategory.PERMANENT.value == "permanent"
        assert ErrorCategory.RESOURCE.value == "resource"
        assert ErrorCategory.VALIDATION.value == "validation"
        assert ErrorCategory.TIMEOUT.value == "timeout"
        assert ErrorCategory.UNKNOWN.value == "unknown"


class TestMetricsCollector:
    
    @pytest.fixture
    def collector(self):
        """Create a metrics collector"""
        return MetricsCollector()
    
    def test_collector_init(self, collector):
        """Test metrics collector initialization"""
        assert collector.start_time is None
        assert isinstance(collector.stage_start_times, dict)
    
    def test_start_validation(self, collector):
        """Test starting validation metrics"""
        collector.start_validation()
        assert collector.start_time is not None
    
    def test_end_validation(self, collector):
        """Test ending validation metrics"""
        collector.start_validation()
        time.sleep(0.01)
        collector.end_validation(status="success", dataset_format="parquet")
        # Should not raise
    
    def test_record_error(self, collector):
        """Test recording an error"""
        collector.record_error(error_type="ValueError", stage="loading")
        # Should not raise
    
    def test_stage_timing(self, collector):
        """Test stage timing"""
        collector.start_stage("preprocessing")
        time.sleep(0.01)
        collector.end_stage("preprocessing")
        # Should not raise
    
    def test_record_dataset_size(self, collector):
        """Test recording dataset size"""
        collector.record_dataset_size(num_rows=10000)
        # Should not raise
    
    def test_record_collapse_score(self, collector):
        """Test recording collapse score"""
        collector.record_collapse_score(score=0.85)
        # Should not raise
    
    def test_record_diversity_score(self, collector):
        """Test recording diversity score"""
        collector.record_diversity_score(score=0.75)
        # Should not raise
    
    def test_update_system_metrics(self, collector):
        """Test updating system metrics"""
        collector.update_system_metrics()
        # Should not raise
    
    def test_get_metrics_text(self, collector):
        """Test getting metrics in Prometheus format"""
        text = collector.get_metrics_text()
        assert isinstance(text, bytes)
    
    def test_get_metrics_content_type(self, collector):
        """Test getting Prometheus content type"""
        content_type = collector.get_metrics_content_type()
        assert isinstance(content_type, str)
        assert "text/plain" in content_type or "openmetrics" in content_type.lower()
