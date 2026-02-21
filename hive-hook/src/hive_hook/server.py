"""FastAPI server that bridges REST endpoints with LangGraph execution.

This module is the runtime entry point for hive-hook agents.  Calling
``start_server(graph, agent_id)`` spins up a FastAPI application with
three endpoints and a permanent background task:

Endpoints:
    ``GET /id``     — returns this agent's identifier.
    ``POST /start`` — accepts an optional JSON body as the initial
        graph state and launches graph execution as a background
        ``asyncio.Task``.
    ``POST /data``  — accepts a ``HiveInboundBaseData`` JSON body and
        publishes it into the ``AsyncBuffer`` so that any ``@hook``
        node waiting on the matching ``data_id`` can proceed.

Background tasks:
    **Outbound forwarder** — launched at server startup, this task
    loops forever calling ``AsyncBuffer.consume_outbound()``.  Each
    item is POSTed to the hive mind at
    ``{HIVE_MIND_ADDRESS}{HIVE_MIND_COMMUICATION_ENDPOINT}``.
"""

from __future__ import annotations

import asyncio
import logging
from typing import Any, Dict, Optional, Set

import httpx
import uvicorn
from fastapi import FastAPI

from hive_hook.buffer import AsyncBuffer, AsyncBufferHandle
from hive_hook.config import HiveConfig
from hive_hook.data import HiveInboundBaseData

logger = logging.getLogger("hive_hook.server")


async def _outbound_forwarder(buffer: AsyncBuffer, config: HiveConfig) -> None:
    """Permanent background task that drains the outbound queue.

    Runs inside the uvicorn event loop.  On each iteration it awaits
    the next ``HiveOutboundBaseData`` item from the buffer's outbound
    queue, serialises it to JSON, and POSTs it to the hive mind.

    Failures are logged but do **not** crash the task — the loop
    continues to process subsequent items.

    Args:
        buffer: The shared ``AsyncBuffer`` instance.
        config: The ``HiveConfig`` containing the hive mind URL.
    """
    hive_mind_url = (
        f"{config.HIVE_MIND_ADDRESS}/{config.HIVE_MIND_COMMUICATION_ENDPOINT}"
    )

    async with httpx.AsyncClient() as client:
        while True:
            item = await buffer.consume_outbound()
            try:
                response = await client.post(
                    hive_mind_url,
                    json=item.model_dump(),
                )
                logger.info(
                    "Outbound → %s (status %s)",
                    item.destination_agent_id,
                    response.status_code,
                )
            except Exception:
                logger.exception(
                    "Failed to forward outbound item to %s",
                    item.destination_agent_id,
                )


def start_server(
    graph: Any,
    agent_id: str,
    host: str = "0.0.0.0",
) -> None:
    """Start the hive-hook FastAPI server.

    This is the main entry point for running an agent.  It:

    1. Loads configuration from environment variables via
       ``HiveConfig.from_env()``.
    2. Creates a shared ``AsyncBuffer``.
    3. Registers a startup hook that launches the outbound forwarder.
    4. Defines the ``/id``, ``/start``, and ``/data`` endpoints.
    5. Calls ``uvicorn.run()`` (blocking).

    The ``/start`` endpoint creates a background ``asyncio.Task`` that
    attaches the buffer to the task's context and then calls
    ``graph.ainvoke(initial_state)``.

    Args:
        graph: A compiled LangGraph graph (``CompiledGraph``).  Must
            support ``.ainvoke(state)``.
        agent_id: A unique identifier for this agent, returned by the
            ``GET /id`` endpoint.
        host: Network interface to bind to.  Defaults to ``"0.0.0.0"``.
    """

    config = HiveConfig.from_env()

    app = FastAPI(title="hive-hook")
    buffer: AsyncBuffer = AsyncBuffer()

    _background_tasks: Set[asyncio.Task] = set()

    @app.on_event("startup")
    async def _start_outbound_forwarder() -> None:
        """Launch the outbound forwarder as a fire-and-forget task."""
        task = asyncio.create_task(_outbound_forwarder(buffer, config))
        _background_tasks.add(task)
        task.add_done_callback(_background_tasks.discard)

    @app.get("/id")
    async def get_id() -> Dict[str, str]:
        """Return this agent's identifier."""
        return {"agent_id": agent_id}

    @app.post("/start")
    async def start(initial_state: Optional[Dict[str, Any]] = None) -> Dict[str, Any]:
        """Kick off a new graph execution.

        Accepts an optional JSON body that becomes the graph's initial
        state.  If omitted, the graph starts with an empty state dict.
        Graph execution runs as a background task so the HTTP response
        returns immediately.
        """
        if initial_state is None:
            initial_state = {}

        async def _run() -> None:
            AsyncBufferHandle.attach(buffer)
            try:
                result = await graph.ainvoke(initial_state)
                logger.info("Graph finished: %s", result)
            except Exception:
                logger.exception("Graph execution failed")

        task = asyncio.create_task(_run())
        _background_tasks.add(task)
        task.add_done_callback(_background_tasks.discard)

        return {"status": "started"}

    @app.post("/data")
    async def receive_data(item: HiveInboundBaseData) -> Dict[str, str]:
        """Receive inbound data and publish it to the buffer.

        The item is deserialised as ``HiveInboundBaseData`` (with extra
        fields preserved) and deposited into the buffer.  Any ``@hook``
        subscriber whose ``data_id`` and ``unique_id`` filters match
        will be woken.
        """
        await buffer.publish_inbound(item)
        return {"status": "received", "data_id": item.data_id}

    uvicorn.run(app, host=host, port=config.HIVE_POD_PORT)
