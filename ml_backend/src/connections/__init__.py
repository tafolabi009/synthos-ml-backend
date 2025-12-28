"""
Connections module for ML Backend
Provides database and cache connections for production use
"""

from .db import (
    DatabaseConnection,
    CacheConnection,
    ConnectionManager,
    get_connection_manager,
    init_connections,
    close_connections,
)

__all__ = [
    'DatabaseConnection',
    'CacheConnection',
    'ConnectionManager',
    'get_connection_manager',
    'init_connections',
    'close_connections',
]
