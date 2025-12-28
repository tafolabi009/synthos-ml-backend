"""
Database and Cache Connections for ML Backend
Production-ready PostgreSQL and Redis connections with connection pooling
"""

import asyncio
import logging
import os
from typing import Optional, Dict, Any
from contextlib import asynccontextmanager
import json

import asyncpg
import redis.asyncio as redis

logger = logging.getLogger(__name__)


class DatabaseConnection:
    """
    PostgreSQL database connection with connection pooling.
    Thread-safe and designed for async operations.
    """
    
    def __init__(self, database_url: str, min_connections: int = 5, max_connections: int = 20):
        self.database_url = database_url
        self.min_connections = min_connections
        self.max_connections = max_connections
        self._pool: Optional[asyncpg.Pool] = None
        self._lock = asyncio.Lock()
    
    async def connect(self) -> None:
        """Establish database connection pool."""
        async with self._lock:
            if self._pool is not None:
                return
            
            try:
                self._pool = await asyncpg.create_pool(
                    self.database_url,
                    min_size=self.min_connections,
                    max_size=self.max_connections,
                    command_timeout=60,
                    statement_cache_size=100,
                )
                logger.info("✅ PostgreSQL connection pool established")
            except Exception as e:
                logger.error(f"Failed to connect to PostgreSQL: {e}")
                raise
    
    async def disconnect(self) -> None:
        """Close database connection pool."""
        async with self._lock:
            if self._pool is not None:
                await self._pool.close()
                self._pool = None
                logger.info("PostgreSQL connection pool closed")
    
    @asynccontextmanager
    async def acquire(self):
        """Acquire a connection from the pool."""
        if self._pool is None:
            await self.connect()
        async with self._pool.acquire() as connection:
            yield connection
    
    async def execute(self, query: str, *args) -> str:
        """Execute a query and return status."""
        async with self.acquire() as conn:
            return await conn.execute(query, *args)
    
    async def fetch(self, query: str, *args) -> list:
        """Fetch all rows from a query."""
        async with self.acquire() as conn:
            return await conn.fetch(query, *args)
    
    async def fetchrow(self, query: str, *args) -> Optional[asyncpg.Record]:
        """Fetch a single row from a query."""
        async with self.acquire() as conn:
            return await conn.fetchrow(query, *args)
    
    async def fetchval(self, query: str, *args) -> Any:
        """Fetch a single value from a query."""
        async with self.acquire() as conn:
            return await conn.fetchval(query, *args)
    
    async def is_healthy(self) -> bool:
        """Check if database connection is healthy."""
        try:
            async with self.acquire() as conn:
                await conn.fetchval("SELECT 1")
            return True
        except Exception as e:
            logger.error(f"Database health check failed: {e}")
            return False
    
    # Job status operations
    async def update_job_status(self, job_id: str, status: str, progress: float = None, error: str = None) -> None:
        """Update job status in database."""
        query = """
            UPDATE jobs 
            SET status = $1, 
                progress = COALESCE($2, progress),
                error_message = $3,
                updated_at = NOW()
            WHERE id = $4
        """
        await self.execute(query, status, progress, error, job_id)
    
    async def get_job(self, job_id: str) -> Optional[Dict[str, Any]]:
        """Get job details from database."""
        query = "SELECT * FROM jobs WHERE id = $1"
        row = await self.fetchrow(query, job_id)
        return dict(row) if row else None
    
    async def save_validation_results(self, validation_id: str, results: Dict[str, Any]) -> None:
        """Save validation results to database."""
        query = """
            UPDATE validations 
            SET status = 'completed',
                diversity_score = $1,
                validation_score = $2,
                collapse_detected = $3,
                collapse_severity = $4,
                metadata = $5,
                completed_at = NOW(),
                updated_at = NOW()
            WHERE id = $6
        """
        await self.execute(
            query,
            results.get('diversity_score'),
            results.get('validation_score'),
            results.get('collapse_detected'),
            results.get('collapse_severity'),
            json.dumps(results.get('metadata', {})),
            validation_id
        )


class CacheConnection:
    """
    Redis cache connection with connection pooling.
    Used for caching validation results and job states.
    """
    
    def __init__(self, redis_url: str, password: str = None, db: int = 0):
        self.redis_url = redis_url
        self.password = password
        self.db = db
        self._client: Optional[redis.Redis] = None
        self._lock = asyncio.Lock()
    
    async def connect(self) -> None:
        """Establish Redis connection."""
        async with self._lock:
            if self._client is not None:
                return
            
            try:
                self._client = redis.Redis.from_url(
                    self.redis_url,
                    password=self.password,
                    db=self.db,
                    decode_responses=True,
                    max_connections=20,
                )
                # Test connection
                await self._client.ping()
                logger.info("✅ Redis connection established")
            except Exception as e:
                logger.error(f"Failed to connect to Redis: {e}")
                raise
    
    async def disconnect(self) -> None:
        """Close Redis connection."""
        async with self._lock:
            if self._client is not None:
                await self._client.close()
                self._client = None
                logger.info("Redis connection closed")
    
    @property
    def client(self) -> redis.Redis:
        """Get Redis client."""
        if self._client is None:
            raise RuntimeError("Redis not connected. Call connect() first.")
        return self._client
    
    async def is_healthy(self) -> bool:
        """Check if Redis connection is healthy."""
        try:
            await self.client.ping()
            return True
        except Exception as e:
            logger.error(f"Redis health check failed: {e}")
            return False
    
    # Cache operations
    async def get(self, key: str) -> Optional[str]:
        """Get a value from cache."""
        return await self.client.get(key)
    
    async def set(self, key: str, value: str, expire: int = None) -> None:
        """Set a value in cache with optional expiration."""
        await self.client.set(key, value, ex=expire)
    
    async def delete(self, key: str) -> None:
        """Delete a key from cache."""
        await self.client.delete(key)
    
    async def get_json(self, key: str) -> Optional[Dict[str, Any]]:
        """Get a JSON value from cache."""
        value = await self.get(key)
        if value:
            return json.loads(value)
        return None
    
    async def set_json(self, key: str, value: Dict[str, Any], expire: int = None) -> None:
        """Set a JSON value in cache."""
        await self.set(key, json.dumps(value), expire)
    
    # Job queue operations
    async def enqueue_job(self, queue_name: str, job_data: Dict[str, Any]) -> None:
        """Add a job to the queue."""
        await self.client.lpush(queue_name, json.dumps(job_data))
    
    async def dequeue_job(self, queue_name: str, timeout: int = 0) -> Optional[Dict[str, Any]]:
        """Get a job from the queue."""
        result = await self.client.brpop(queue_name, timeout=timeout)
        if result:
            return json.loads(result[1])
        return None
    
    async def get_queue_length(self, queue_name: str) -> int:
        """Get the length of a queue."""
        return await self.client.llen(queue_name)
    
    # Pub/Sub for job status updates
    async def publish_status(self, channel: str, message: Dict[str, Any]) -> None:
        """Publish a status update to a channel."""
        await self.client.publish(channel, json.dumps(message))
    
    async def subscribe(self, channel: str):
        """Subscribe to a channel for status updates."""
        pubsub = self.client.pubsub()
        await pubsub.subscribe(channel)
        return pubsub


class ConnectionManager:
    """
    Manages all connections for the ML Backend service.
    Singleton pattern for consistent connection management.
    """
    
    _instance: Optional['ConnectionManager'] = None
    _lock = asyncio.Lock()
    
    def __init__(self):
        self.db: Optional[DatabaseConnection] = None
        self.cache: Optional[CacheConnection] = None
        self._initialized = False
    
    @classmethod
    async def get_instance(cls) -> 'ConnectionManager':
        """Get singleton instance."""
        async with cls._lock:
            if cls._instance is None:
                cls._instance = ConnectionManager()
            return cls._instance
    
    async def initialize(
        self,
        database_url: str = None,
        redis_url: str = None,
        redis_password: str = None
    ) -> None:
        """Initialize all connections."""
        if self._initialized:
            return
        
        # Get configuration from environment if not provided
        database_url = database_url or os.getenv('DATABASE_URL')
        redis_url = redis_url or os.getenv('REDIS_URL')
        redis_password = redis_password or os.getenv('REDIS_PASSWORD')
        
        # Initialize database if URL provided
        if database_url:
            self.db = DatabaseConnection(database_url)
            await self.db.connect()
        
        # Initialize cache if URL provided
        if redis_url:
            self.cache = CacheConnection(redis_url, redis_password)
            await self.cache.connect()
        
        self._initialized = True
        logger.info("✅ All connections initialized")
    
    async def close(self) -> None:
        """Close all connections."""
        if self.db:
            await self.db.disconnect()
        if self.cache:
            await self.cache.disconnect()
        self._initialized = False
        logger.info("All connections closed")
    
    async def health_check(self) -> Dict[str, bool]:
        """Check health of all connections."""
        health = {}
        
        if self.db:
            health['database'] = await self.db.is_healthy()
        
        if self.cache:
            health['cache'] = await self.cache.is_healthy()
        
        return health


# Global connection manager instance
_connection_manager: Optional[ConnectionManager] = None


async def get_connection_manager() -> ConnectionManager:
    """Get the global connection manager instance."""
    global _connection_manager
    if _connection_manager is None:
        _connection_manager = await ConnectionManager.get_instance()
    return _connection_manager


async def init_connections(database_url: str = None, redis_url: str = None) -> ConnectionManager:
    """Initialize global connections."""
    manager = await get_connection_manager()
    await manager.initialize(database_url, redis_url)
    return manager


async def close_connections() -> None:
    """Close global connections."""
    global _connection_manager
    if _connection_manager:
        await _connection_manager.close()
        _connection_manager = None
