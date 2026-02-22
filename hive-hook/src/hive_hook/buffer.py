"""Asynchronous pub-sub buffer for inter-agent data exchange.

The ``AsyncBuffer`` is the central data structure that decouples REST
endpoint handlers from LangGraph node execution:

- **Inbound path**: The ``/data`` endpoint calls ``publish_inbound()``
  to deposit items.  ``@hook``-decorated nodes call ``collect_inbound()``
  to block until their required ``data_id``\\ s are satisfied.
- **Outbound path**: Graph nodes call ``send()`` (which calls
  ``publish_outbound()``).  A permanent server task calls
  ``consume_outbound()`` in a loop to drain and forward items.

The buffer is made available to graph nodes via ``AsyncBufferHandle``,
which stores the active buffer instance in a ``contextvars.ContextVar``
so that each ``asyncio.Task`` (graph execution) can discover it
without explicit parameter passing.
"""

from __future__ import annotations

import asyncio
import contextvars
from collections import defaultdict, deque
from typing import Deque, Dict, Generic, List, Optional, Set, Tuple, TypeVar

from hive_hook.data import HiveInboundBaseData, HiveOutboundBaseData

T = TypeVar("T", bound=HiveInboundBaseData)

Subscriber = Tuple[asyncio.Event, Set[str], Optional[str]]
"""Type alias for an inbound subscriber entry.

A 3-tuple of:
    0. ``asyncio.Event`` — signalled when a matching item arrives.
    1. ``Set[str]``      — the ``data_id`` values this subscriber cares about.
    2. ``Optional[str]`` — an optional ``unique_id`` filter.
"""

_active_async_buffer: contextvars.ContextVar[Optional[AsyncBuffer]] = (
    contextvars.ContextVar("active_async_buffer", default=None)
)


class AsyncBufferHandle:
    """Context-variable-based accessor for the active ``AsyncBuffer``.

    The server attaches a buffer before launching a graph execution
    task.  Graph nodes (and the ``@hook`` decorator) call ``discover()``
    to retrieve it.

    Because ``asyncio.create_task`` copies the current
    ``contextvars.Context``, the buffer propagates automatically to all
    child tasks within a single graph execution.
    """

    @staticmethod
    def attach(buffer: AsyncBuffer) -> None:
        """Bind *buffer* to the current async context.

        Must be called before ``asyncio.create_task`` so that the
        child task inherits the reference.
        """
        _active_async_buffer.set(buffer)

    @staticmethod
    def discover() -> AsyncBuffer:
        """Retrieve the buffer from the current async context.

        Raises:
            RuntimeError: If no buffer has been attached.
        """
        buffer = _active_async_buffer.get()

        if buffer is None:
            raise RuntimeError("AsyncBuffer not attached")

        return buffer


class AsyncBuffer(Generic[T]):
    """Thread-safe (asyncio-safe) dual-channel message buffer.

    The buffer maintains two independent channels:

    **Inbound channel** — keyed mailbox system:
        Items are stored in per-``data_id`` deques.  Subscribers
        register interest in specific ``data_id``\\ s (with an optional
        ``unique_id`` filter) and are woken via ``asyncio.Event`` when
        a matching item arrives.

    **Outbound channel** — simple FIFO queue:
        Items are placed by graph nodes and consumed by the server's
        permanent forwarder task.

    All mutations are protected by a single ``asyncio.Lock`` to
    guarantee consistency across concurrent subscribers and publishers.
    """

    def __init__(self) -> None:
        self.__inbound_store: Dict[str, Deque[T]] = defaultdict(deque)
        self.__outbound_queue: asyncio.Queue[HiveOutboundBaseData] = asyncio.Queue()
        self.__inbound_subscribers: List[Subscriber] = []
        self.__lock = asyncio.Lock()

    async def subscribe_inbound(
        self, data_ids: List[str], unique_id: Optional[str] = None
    ) -> asyncio.Event:
        """Register interest in one or more ``data_id`` values.

        Returns an ``asyncio.Event`` that will be set whenever a
        matching item is published.  The caller is responsible for
        clearing and re-waiting on the event as needed.

        Args:
            data_ids: The ``data_id`` values to listen for.
            unique_id: If provided, only items whose ``unique_id``
                matches will trigger the event.

        Returns:
            An ``asyncio.Event`` that is set on each matching publish.
        """
        event = asyncio.Event()

        async with self.__lock:
            self.__inbound_subscribers.append((event, set(data_ids), unique_id))

        return event

    async def unsubscribe_inbound(self, event: asyncio.Event) -> None:
        """Remove all subscriber entries associated with *event*."""
        async with self.__lock:
            self.__inbound_subscribers = [
                (e, d, u) for (e, d, u) in self.__inbound_subscribers if e != event
            ]

    async def publish_inbound(self, item: T) -> None:
        """Deposit an inbound item and notify matching subscribers.

        The item is appended to the deque for its ``data_id``.  Every
        subscriber whose ``data_id`` set contains the item's
        ``data_id`` (and whose ``unique_id`` filter matches, if set)
        has its event signalled.

        Args:
            item: The inbound data item to store.
        """
        async with self.__lock:
            self.__inbound_store[item.data_id].append(item)

            for event, data_ids, unique_id in self.__inbound_subscribers:
                if item.data_id not in data_ids:
                    continue

                if unique_id is not None and unique_id != item.unique_id:
                    continue

                event.set()

    async def collect_inbound(
        self, data_ids: List[str], unique_id: Optional[str] = None
    ) -> Dict[str, T]:
        """Block until one item per *data_id* is available, then return them.

        This is the primary primitive used by the ``@hook`` decorator.
        It subscribes, then enters a loop:

        1. Check whether every requested ``data_id`` has at least one
           matching item (respecting ``unique_id`` if given).
        2. If all are satisfied, atomically pop the matched items from
           the store and return them as ``{data_id: item}``.
        3. Otherwise, clear the event and wait for the next publish
           notification.

        The subscription is always cleaned up in a ``finally`` block.

        Args:
            data_ids: The ``data_id`` values to collect.
            unique_id: If provided, only items whose ``unique_id``
                matches are considered.

        Returns:
            A dict mapping each ``data_id`` to its first matching item.
        """
        event = await self.subscribe_inbound(data_ids, unique_id)

        try:
            while True:
                collected: Dict[str, T] = {}

                async with self.__lock:
                    for data_id in data_ids:
                        mailbox = self.__inbound_store.get(data_id)
                        if mailbox is None:
                            break

                        match: Optional[T] = None
                        for item in mailbox:
                            if unique_id is None or item.unique_id == unique_id:
                                match = item
                                break

                        if match is None:
                            break

                        collected[data_id] = match

                    if len(collected) == len(data_ids):
                        for data_id, item in collected.items():
                            self.__inbound_store[data_id].remove(item)
                        return collected

                event.clear()
                await event.wait()
        finally:
            await self.unsubscribe_inbound(event)

    async def publish_outbound(self, item: HiveOutboundBaseData) -> None:
        """Enqueue an outbound item for forwarding to the hive mind.

        Args:
            item: The outbound data item to send.
        """
        await self.__outbound_queue.put(item)

    async def consume_outbound(self) -> HiveOutboundBaseData:
        """Block until the next outbound item is available, then return it.

        Intended to be called in a ``while True`` loop by the server's
        permanent forwarder task.

        Returns:
            The next ``HiveOutboundBaseData`` item from the queue.
        """
        return await self.__outbound_queue.get()
