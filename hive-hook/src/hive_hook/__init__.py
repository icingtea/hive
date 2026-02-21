"""hive-hook — LangGraph inter-agent communication via REST.

This package enables LangGraph agents to pause graph execution at
specific nodes, wait for external data delivered through REST APIs,
and send outbound messages to other agents via a central hive mind.

Core components:
    ``HiveInboundBaseData``
        Base pydantic model for data arriving from external sources.
        Subclass this to define typed payloads.
    ``HiveOutboundBaseData``
        Base pydantic model for data sent to other agents.
    ``EndpointEnum``
        Enum for destination endpoint types (``"start"`` / ``"data"``).
    ``@hook``
        Decorator that pauses a LangGraph node until required
        ``data_id``\\s arrive in the inbound buffer.
    ``hive_data``
        Context-variable accessor for reading collected inbound data
        inside a ``@hook``-decorated node.
    ``send()``
        Coroutine for pushing outbound data to other agents.
    ``start_server()``
        Entry point that launches the FastAPI server, wires up the
        buffer, and starts the outbound forwarder.
    ``AsyncBuffer``
        The shared inbound/outbound message buffer.
    ``AsyncBufferHandle``
        Context-variable handle for discovering the buffer from within
        graph nodes.
    ``HiveConfig``
        Frozen dataclass loaded from environment variables.
"""

from hive_hook.buffer import AsyncBuffer, AsyncBufferHandle
from hive_hook.config import HiveConfig
from hive_hook.data import EndpointEnum, HiveInboundBaseData, HiveOutboundBaseData
from hive_hook.decorator import hive_data, hive_hook
from hive_hook.outbound import send_to_hive
from hive_hook.server import start_server

__all__ = [
    "AsyncBuffer",
    "AsyncBufferHandle",
    "EndpointEnum",
    "HiveConfig",
    "HiveInboundBaseData",
    "HiveOutboundBaseData",
    "hive_data",
    "hive_hook",
    "send_to_hive",
    "start_server",
]
