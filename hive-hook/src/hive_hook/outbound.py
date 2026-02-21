"""Outbound messaging helper for graph nodes.

Provides the ``send()`` coroutine that graph nodes call to enqueue
data destined for other agents.  The data is placed on the
``AsyncBuffer``\\ 's outbound queue, where the server's permanent
forwarder task picks it up and POSTs it to the hive mind.

Example::

    from hive_hook import send, EndpointEnum

    async def my_node(state: dict) -> dict:
        await send(
            destination_agent_id="other_agent",
            destination_agent_endpoint=EndpointEnum.DATA,
            payload={"data_id": "result", "unique_id": "x", "value": 42},
        )
        return state
"""

from __future__ import annotations

from typing import Any, Dict

from hive_hook.buffer import AsyncBufferHandle
from hive_hook.data import EndpointEnum, HiveOutboundBaseData


async def send_to_hive(
    destination_agent_id: str,
    destination_agent_endpoint: EndpointEnum,
    payload: Dict[str, Any],
) -> None:
    """Send data to another agent via the hive mind.

    Discovers the ``AsyncBuffer`` from the current async context and
    enqueues a ``HiveOutboundBaseData`` item.  The server's background
    forwarder task will POST the serialised item to the hive mind,
    which routes it to the destination agent.

    This function must be called from within a graph node (or any
    async context where ``AsyncBufferHandle.attach()`` has been called).

    Args:
        destination_agent_id: Unique identifier of the target agent.
        destination_agent_endpoint: Which endpoint on the target agent
            should receive the payload.  Use ``EndpointEnum.START`` to
            trigger a new graph execution, or ``EndpointEnum.DATA`` to
            feed into an existing one.
        payload: Arbitrary JSON-serialisable dict to deliver.  For
            ``EndpointEnum.DATA`` targets this should include at least
            ``data_id`` (and optionally ``unique_id``) so that the
            receiving agent's ``@hook`` nodes can match on it.

    Raises:
        RuntimeError: If no ``AsyncBuffer`` is attached to the current
            context (i.e. called outside of a graph execution).
    """
    buffer = AsyncBufferHandle.discover()
    item = HiveOutboundBaseData(
        destination_agent_id=destination_agent_id,
        destination_agent_endpoint=destination_agent_endpoint,
        payload=payload,
    )
    await buffer.publish_outbound(item)
