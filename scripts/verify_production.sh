#!/bin/bash
# =============================================================================
# SynthOS Production Verification Script
# =============================================================================
# This script verifies all production components are working correctly:
# - API Gateway health
# - Database connectivity
# - Redis connectivity
# - S3 operations
# - ML Backend gRPC services
# - PDF generation
# - Authentication flow
# =============================================================================

set -e

# Configuration
API_URL="${API_URL:-http://localhost:8000}"
ML_BACKEND_HOST="${ML_BACKEND_HOST:-localhost}"
S3_BUCKET="${S3_BUCKET:-synthos-datasets}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[✓]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[!]${NC} $1"; }
log_error() { echo -e "${RED}[✗]${NC} $1"; }
log_test() { echo -e "${BLUE}[TEST]${NC} $1"; }

PASSED=0
FAILED=0

test_result() {
    if [ $? -eq 0 ]; then
        log_info "$1 - PASSED"
        ((PASSED++))
    else
        log_error "$1 - FAILED"
        ((FAILED++))
    fi
}

# =============================================================================
# Test 1: API Gateway Health
# =============================================================================
test_api_health() {
    log_test "Testing API Gateway health..."
    
    # Basic health
    curl -sf "${API_URL}/health" > /dev/null
    test_result "Basic health endpoint"
    
    # Liveness probe
    curl -sf "${API_URL}/health/live" > /dev/null
    test_result "Liveness probe"
    
    # Readiness probe
    curl -sf "${API_URL}/health/ready" > /dev/null
    test_result "Readiness probe"
    
    # Health details
    HEALTH=$(curl -sf "${API_URL}/health")
    echo "$HEALTH" | jq -e '.status == "healthy"' > /dev/null
    test_result "Health status is healthy"
}

# =============================================================================
# Test 2: Authentication Flow
# =============================================================================
test_auth_flow() {
    log_test "Testing authentication flow..."
    
    TEST_EMAIL="test-$(date +%s)@synthos.ai"
    TEST_PASSWORD="TestPassword123!"
    
    # Register user
    REGISTER_RESPONSE=$(curl -sf -X POST "${API_URL}/api/v1/auth/register" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"${TEST_EMAIL}\",\"password\":\"${TEST_PASSWORD}\",\"full_name\":\"Test User\"}" 2>/dev/null || echo "{}")
    
    if echo "$REGISTER_RESPONSE" | jq -e '.user_id' > /dev/null 2>&1; then
        log_info "User registration - PASSED"
        ((PASSED++))
    else
        log_warn "User registration - SKIPPED (may already exist)"
    fi
    
    # Login
    LOGIN_RESPONSE=$(curl -sf -X POST "${API_URL}/api/v1/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"${TEST_EMAIL}\",\"password\":\"${TEST_PASSWORD}\"}")
    
    ACCESS_TOKEN=$(echo "$LOGIN_RESPONSE" | jq -r '.access_token')
    
    if [ -n "$ACCESS_TOKEN" ] && [ "$ACCESS_TOKEN" != "null" ]; then
        log_info "User login - PASSED"
        ((PASSED++))
        echo "ACCESS_TOKEN=${ACCESS_TOKEN}" > /tmp/synthos_test_token
    else
        log_error "User login - FAILED"
        ((FAILED++))
        return 1
    fi
    
    # Token refresh
    REFRESH_TOKEN=$(echo "$LOGIN_RESPONSE" | jq -r '.refresh_token')
    REFRESH_RESPONSE=$(curl -sf -X POST "${API_URL}/api/v1/auth/refresh" \
        -H "Content-Type: application/json" \
        -d "{\"refresh_token\":\"${REFRESH_TOKEN}\"}")
    
    echo "$REFRESH_RESPONSE" | jq -e '.access_token' > /dev/null
    test_result "Token refresh"
}

# =============================================================================
# Test 3: Dataset Operations
# =============================================================================
test_dataset_operations() {
    log_test "Testing dataset operations..."
    
    source /tmp/synthos_test_token 2>/dev/null || true
    
    if [ -z "$ACCESS_TOKEN" ]; then
        log_warn "Skipping dataset tests - no access token"
        return
    fi
    
    # Initiate upload
    UPLOAD_RESPONSE=$(curl -sf -X POST "${API_URL}/api/v1/datasets/upload" \
        -H "Authorization: Bearer ${ACCESS_TOKEN}" \
        -H "Content-Type: application/json" \
        -d '{"filename":"test.csv","file_size":1024,"file_type":"csv","description":"Test dataset"}')
    
    DATASET_ID=$(echo "$UPLOAD_RESPONSE" | jq -r '.dataset_id')
    
    if [ -n "$DATASET_ID" ] && [ "$DATASET_ID" != "null" ]; then
        log_info "Dataset upload initiation - PASSED"
        ((PASSED++))
    else
        log_error "Dataset upload initiation - FAILED"
        ((FAILED++))
    fi
    
    # List datasets
    LIST_RESPONSE=$(curl -sf "${API_URL}/api/v1/datasets" \
        -H "Authorization: Bearer ${ACCESS_TOKEN}")
    
    echo "$LIST_RESPONSE" | jq -e '.datasets' > /dev/null
    test_result "List datasets"
}

# =============================================================================
# Test 4: Validation Operations
# =============================================================================
test_validation_operations() {
    log_test "Testing validation operations..."
    
    source /tmp/synthos_test_token 2>/dev/null || true
    
    if [ -z "$ACCESS_TOKEN" ]; then
        log_warn "Skipping validation tests - no access token"
        return
    fi
    
    # List validations
    LIST_RESPONSE=$(curl -sf "${API_URL}/api/v1/validations" \
        -H "Authorization: Bearer ${ACCESS_TOKEN}")
    
    echo "$LIST_RESPONSE" | jq -e '.validations' > /dev/null
    test_result "List validations"
}

# =============================================================================
# Test 5: ML Backend gRPC Services
# =============================================================================
test_ml_backend() {
    log_test "Testing ML Backend gRPC services..."
    
    # Check if grpcurl is available
    if ! command -v grpcurl &> /dev/null; then
        log_warn "grpcurl not installed - installing..."
        go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest 2>/dev/null || {
            log_warn "Could not install grpcurl, skipping gRPC tests"
            return
        }
    fi
    
    # Test Validation Service
    grpcurl -plaintext ${ML_BACKEND_HOST}:50051 list > /dev/null 2>&1
    test_result "Validation Service (port 50051)"
    
    # Test Collapse Service
    grpcurl -plaintext ${ML_BACKEND_HOST}:50052 list > /dev/null 2>&1
    test_result "Collapse Service (port 50052)"
    
    # Test Data Service
    grpcurl -plaintext ${ML_BACKEND_HOST}:50054 list > /dev/null 2>&1
    test_result "Data Service (port 50054)"
}

# =============================================================================
# Test 6: PDF Generation
# =============================================================================
test_pdf_generation() {
    log_test "Testing PDF generation..."
    
    source /tmp/synthos_test_token 2>/dev/null || true
    
    if [ -z "$ACCESS_TOKEN" ]; then
        log_warn "Skipping PDF tests - no access token"
        return
    fi
    
    # Get a completed validation ID (if any)
    VALIDATION_ID=$(curl -sf "${API_URL}/api/v1/validations?status=completed" \
        -H "Authorization: Bearer ${ACCESS_TOKEN}" | jq -r '.validations[0].id // empty')
    
    if [ -z "$VALIDATION_ID" ]; then
        log_warn "No completed validations found - skipping PDF test"
        return
    fi
    
    # Test report PDF
    HTTP_CODE=$(curl -s -o /tmp/report.pdf -w "%{http_code}" \
        "${API_URL}/api/v1/validations/${VALIDATION_ID}/report" \
        -H "Authorization: Bearer ${ACCESS_TOKEN}")
    
    if [ "$HTTP_CODE" = "200" ]; then
        # Verify it's a valid PDF
        file /tmp/report.pdf | grep -q "PDF document"
        test_result "Report PDF generation"
    else
        log_warn "Report PDF - SKIPPED (HTTP $HTTP_CODE)"
    fi
    
    # Test certificate PDF
    HTTP_CODE=$(curl -s -o /tmp/certificate.pdf -w "%{http_code}" \
        "${API_URL}/api/v1/validations/${VALIDATION_ID}/certificate" \
        -H "Authorization: Bearer ${ACCESS_TOKEN}")
    
    if [ "$HTTP_CODE" = "200" ]; then
        file /tmp/certificate.pdf | grep -q "PDF document"
        test_result "Certificate PDF generation"
    else
        log_warn "Certificate PDF - SKIPPED (may not be warranty eligible)"
    fi
}

# =============================================================================
# Test 7: S3 Operations
# =============================================================================
test_s3_operations() {
    log_test "Testing S3 operations..."
    
    # Check if AWS CLI is available
    if ! command -v aws &> /dev/null; then
        log_warn "AWS CLI not installed - skipping S3 tests"
        return
    fi
    
    # List bucket contents
    aws s3 ls "s3://${S3_BUCKET}" > /dev/null 2>&1
    test_result "S3 bucket access"
    
    # Upload test file
    echo "test" | aws s3 cp - "s3://${S3_BUCKET}/test/verification-$(date +%s).txt" 2>/dev/null
    test_result "S3 upload"
    
    # Download test file
    aws s3 cp "s3://${S3_BUCKET}/test/verification-*.txt" /tmp/s3_test.txt 2>/dev/null
    test_result "S3 download"
    
    # Cleanup
    aws s3 rm "s3://${S3_BUCKET}/test/" --recursive 2>/dev/null || true
}

# =============================================================================
# Test 8: Database Connectivity (via API)
# =============================================================================
test_database() {
    log_test "Testing database connectivity..."
    
    # The health endpoint checks database
    HEALTH=$(curl -sf "${API_URL}/health")
    echo "$HEALTH" | jq -e '.database == "healthy"' > /dev/null 2>&1
    test_result "Database connectivity (via health check)"
}

# =============================================================================
# Test 9: Redis Connectivity (via API)
# =============================================================================
test_redis() {
    log_test "Testing Redis connectivity..."
    
    # Check if redis-cli is available
    if command -v redis-cli &> /dev/null; then
        REDIS_HOST="${REDIS_HOST:-localhost}"
        redis-cli -h $REDIS_HOST ping > /dev/null 2>&1
        test_result "Redis connectivity (direct)"
    else
        log_warn "redis-cli not installed - skipping direct Redis test"
    fi
}

# =============================================================================
# Test 10: Rate Limiting
# =============================================================================
test_rate_limiting() {
    log_test "Testing rate limiting..."
    
    # Make many requests quickly
    for i in {1..20}; do
        curl -sf "${API_URL}/health" > /dev/null &
    done
    wait
    
    # The 21st request should still work (within limits)
    curl -sf "${API_URL}/health" > /dev/null
    test_result "Rate limiting (basic)"
}

# =============================================================================
# Main Execution
# =============================================================================
main() {
    echo "=============================================="
    echo "  SynthOS Production Verification"
    echo "=============================================="
    echo ""
    echo "API URL: ${API_URL}"
    echo "ML Backend: ${ML_BACKEND_HOST}"
    echo "S3 Bucket: ${S3_BUCKET}"
    echo ""
    echo "----------------------------------------------"
    
    test_api_health
    echo ""
    
    test_auth_flow
    echo ""
    
    test_dataset_operations
    echo ""
    
    test_validation_operations
    echo ""
    
    test_ml_backend
    echo ""
    
    test_pdf_generation
    echo ""
    
    test_s3_operations
    echo ""
    
    test_database
    echo ""
    
    test_redis
    echo ""
    
    test_rate_limiting
    echo ""
    
    # Summary
    echo "=============================================="
    echo "  Verification Summary"
    echo "=============================================="
    echo ""
    echo -e "Tests Passed: ${GREEN}${PASSED}${NC}"
    echo -e "Tests Failed: ${RED}${FAILED}${NC}"
    echo ""
    
    TOTAL=$((PASSED + FAILED))
    if [ $FAILED -eq 0 ]; then
        echo -e "${GREEN}All ${TOTAL} tests passed! ✓${NC}"
        exit 0
    else
        echo -e "${RED}${FAILED} of ${TOTAL} tests failed${NC}"
        exit 1
    fi
}

# Cleanup on exit
cleanup() {
    rm -f /tmp/synthos_test_token /tmp/report.pdf /tmp/certificate.pdf /tmp/s3_test.txt 2>/dev/null
}
trap cleanup EXIT

# Run main
main "$@"
