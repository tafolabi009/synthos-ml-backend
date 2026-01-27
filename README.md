# Synthos ML Backend

**Advanced Spectral Neural Networks for AI Training Data Validation**

> ⚠️ **Status: Alpha** - Core implementation complete, comprehensive testing in progress. Not production ready.

Microservices architecture for ML validation platform that detects model collapse **before** training begins.

---

## 🎯 What We Do

Synthos is the **first validation and certification platform** that guarantees AI training data won't cause model collapse. We prevent $100M training failures before they happen.

**Core Value Proposition:** Don't waste millions on training data that will collapse your model.

---

## 📦 Project Structure

```
├── ml_backend/              # Port 50051/50052 - Unified ML Service (Validation + Collapse)
├── validation_service/      # Port 50051 - Standalone Cascade Training & Diversity Analysis
├── collapse_service/        # Port 50053 - Standalone Collapse Detection & Recommendations  
├── go_backend/              # Port 8000  - REST API Gateway (Fiber)
├── job_orchestrator/        # Port 8080  - Job Queue Management (REST + gRPC)
├── data_service/            # Port 50054 - Dataset Management
├── admin_dashboard/         # Admin UI (Go + HTML)
├── monitoring/              # Prometheus + Grafana configs
├── proto/                   # Protocol Buffer definitions
├── migrations/              # Database migrations
└── scripts/                 # Deployment & utility scripts
```

---

## 🚀 Quick Start

### Prerequisites
- Docker & Docker Compose
- (Optional) NVIDIA GPU with CUDA for GPU acceleration

### Start All Services

```bash
# Clone and setup
git clone https://github.com/tafolabi009/ml_backend.git
cd ml_backend

# Copy environment file and configure
cp .env.example .env
# Edit .env with your secrets (POSTGRES_PASSWORD, REDIS_PASSWORD, JWT_SECRET)

# Start all services
docker-compose up -d

# View logs
docker-compose logs -f ml-backend
docker-compose logs -f api-gateway

# Check health
curl http://localhost:8000/health

# Stop all
docker-compose down
```

---

## 🏗️ Services Overview

### Python ML Services

| Service | Port | Description |
|---------|------|-------------|
| **ml-backend** | 50051, 50052, 50054 | Unified ML service with Validation + Collapse engines |
| **validation_service** | 50051 | Standalone cascade training (18 models, 3 tiers) |
| **collapse_service** | 50053 | Standalone 8-dimensional collapse detection |

**Key ML Features:**
- 🧠 **Resonance NN Architecture** - FFT-based spectral processing, O(n log n) complexity
- 📊 **18-Model Cascade Training** - Tiny (76M), Small (454M), Base (983M) params
- 🔍 **8-Dimensional Collapse Detection** - Mode collapse, spectral degradation, gradient pathology, etc.
- 🎯 **Gradient-based Localization** - Pinpoint exact problematic rows
- 💡 **Actionable Recommendations** - Prioritized fixes with cost-benefit analysis

### Go Services

| Service | Port | Description |
|---------|------|-------------|
| **api-gateway** | 8000 | REST API Gateway (Fiber framework) |
| **job-orchestrator** | 8080 (REST), 50053 (gRPC) | Central pipeline controller |
| **data-service** | 50054 | Dataset storage and retrieval |
| **admin-dashboard** | 8090 | Admin UI for monitoring |

**Key API Features:**
- 🔐 JWT authentication with refresh tokens
- 📤 S3-compatible file uploads (MinIO/AWS)
- 📊 Real-time job progress tracking
- 🔄 gRPC communication with ML services

### Infrastructure

| Service | Port | Description |
|---------|------|-------------|
| **PostgreSQL** | 5432 | Primary database |
| **Redis** | 6379 | Cache & job queue |
| **MinIO** | 9000/9001 | S3-compatible object storage |
| **Prometheus** | 9090 | Metrics collection |
| **Grafana** | 3000 | Metrics visualization |

---

## 🔌 API Endpoints

### Authentication
- `POST /api/v1/auth/register` - Register new user
- `POST /api/v1/auth/login` - Login and get JWT token
- `POST /api/v1/auth/refresh` - Refresh access token

### Datasets
- `POST /api/v1/datasets/upload` - Initiate upload, get signed URL
- `POST /api/v1/datasets/:id/complete` - Mark upload complete
- `GET /api/v1/datasets` - List datasets
- `GET /api/v1/datasets/:id` - Get dataset details
- `DELETE /api/v1/datasets/:id` - Delete dataset

### Validations
- `POST /api/v1/validations/create` - Create validation job
- `GET /api/v1/validations` - List validations
- `GET /api/v1/validations/:id` - Get validation results
- `GET /api/v1/validations/:id/collapse-details` - Get collapse analysis
- `GET /api/v1/validations/:id/recommendations` - Get fix recommendations

### Analytics
- `GET /api/v1/analytics/usage` - Usage statistics
- `GET /api/v1/analytics/validation-history` - Historical data

---

## 💻 Development

### Run ML Service Locally

```bash
cd ml_backend
pip install -r requirements.txt
python server.py
```

### Run API Gateway Locally

```bash
cd go_backend
go mod download
go run cmd/api/main.go
```

### Run Tests

```bash
# ML Backend tests
cd ml_backend
./run_tests.sh

# Or manually
pytest tests/ -v --cov=src --cov-report=html
```

### Rebuild Services

```bash
# Rebuild specific service
docker-compose build ml-backend
docker-compose up -d ml-backend

# Rebuild all
docker-compose build
docker-compose up -d
```

---

## 📊 Validation Pipeline

The validation process runs through 6 automated stages:

```
Data → Diversity → Cascade → Collapse → Localization → Recommendations
  ↓        ↓          ↓          ↓            ↓               ↓
Load    Analyze    Train     Detect      Pinpoint         Fix
```

**Total Time:** ~85 seconds for 1M rows

| Stage | Time (1M rows) | Description |
|-------|----------------|-------------|
| 1. Data Loading | ~5s | Load any format (CSV, Parquet, JSON, HDF5, Arrow) |
| 2. Diversity Analysis | ~10s | Semantic, statistical, structural diversity scoring |
| 3. Cascade Training | ~30s | Train 18 Resonance NN models (10 tiny + 5 small + 3 base) |
| 4. Collapse Detection | ~15s | 8-dimensional collapse analysis |
| 5. Problem Localization | ~20s | Gradient-based row scoring |
| 6. Recommendations | ~5s | Prioritized fixes with cost-benefit |

---

## 🔒 Security

- **JWT Authentication** - Access tokens (15 min) + Refresh tokens (30 days)
- **mTLS** - Mutual TLS for service-to-service communication
- **Password Hashing** - bcrypt with cost 10
- **Rate Limiting** - Per-customer tier limits

---

## 📚 Documentation

- [ML Backend Docs](ml_backend/docs/README.md) - Detailed ML architecture
- [Go Backend Implementation](go_backend/GO_BACKEND_IMPLEMENTATION.md) - API Gateway details
- [Testing Guide](ml_backend/TESTING_GUIDE.md) - How to run and write tests
- [RunPod Deployment](ml_backend/RUNPOD_DEPLOYMENT.md) - GPU cloud deployment
- [API Architecture](ml_backend/docs/synthos-api-architecture.md) - Complete API specification

---

## 🛠️ Tech Stack

**Languages:** Python 3.10+, Go 1.21+

**ML/Data:**
- PyTorch 2.0+
- Resonance Neural Networks (custom FFT-based architecture)
- NumPy, Pandas, SciPy, scikit-learn
- FAISS (vector search)

**Backend:**
- Fiber (Go web framework)
- gRPC + Protocol Buffers
- PostgreSQL 15, Redis 7

**Infrastructure:**
- Docker & Docker Compose
- MinIO (S3-compatible)
- Prometheus + Grafana
- Jaeger (distributed tracing)

---

## 📄 License

MIT License - See [ml_backend/LICENSE](ml_backend/LICENSE)

---

## 👥 Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Run tests (`./run_tests.sh`)
4. Commit your changes (`git commit -m 'Add amazing feature'`)
5. Push to the branch (`git push origin feature/amazing-feature`)
6. Open a Pull Request
