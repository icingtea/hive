"""``@hook`` decorator and ``hive_data`` context accessor.

This module provides the mechanism for pausing a LangGraph node until
external data arrives in the ``AsyncBuffer``:

- **``@hook``** — a decorator that wraps an async graph node function.
  Before the node body executes, it subscribes to the buffer, waits
  for one item per declared ``data_id``, re-validates each item to the
  declared subclass type, and stores the results in a ``ContextVar``.
- **``hive_data``** — a typed accessor that graph nodes use to
  retrieve the collected data by ``data_id`` key.

Example::

    from hive_hook import hook, hive_data

    class PaymentConfirmation(HiveInboundBaseData):
        amount: float
        currency: str

    @hook({"payment": PaymentConfirmation})
    async def await_payment(state: dict) -> dict:
        payment = hive_data.get("payment", PaymentConfirmation)
        print(payment.amount)
        ...
"""

from __future__ import annotations

import contextvars
from functools import wraps
from typing import (
    Any,
    Callable,
    Dict,
    Optional,
    Type,
    TypeVar,
    cast,
    overload,
)

from hive_hook.buffer import AsyncBufferHandle
from hive_hook.data import HiveInboundBaseData

T = TypeVar("T", bound=HiveInboundBaseData)


class _HiveDataAccessor:
    """Typed accessor for data collected by ``@hook``.

    Wraps a ``ContextVar`` holding the collected data dict.  The
    ``@hook`` decorator sets this var before the node function runs and
    resets it afterwards, ensuring each concurrent graph execution
    operates on its own isolated data.

    Usage::

        from hive_hook import hive_data

        # Untyped — returns HiveInboundBaseData
        item = hive_data.get("payment")

        # Typed — returns PaymentConfirmation (cast, already validated)
        item = hive_data.get("payment", PaymentConfirmation)
    """

    _var: contextvars.ContextVar[Dict[str, HiveInboundBaseData]] = (
        contextvars.ContextVar("hive_data")
    )

    @overload
    def get(self, data_id: str) -> HiveInboundBaseData: ...

    @overload
    def get(self, data_id: str, data_type: Type[T]) -> T: ...

    def get(self, data_id: str, data_type: Optional[Type[Any]] = None) -> Any:
        """Retrieve a collected inbound item by its ``data_id``.

        Args:
            data_id: The key to look up (must match one of the keys
                declared in the ``@hook`` type mapping).
            data_type: Optional type for static narrowing.  At runtime
                the item is already the correct type (re-validated by
                the decorator), so this is a no-op cast for the type
                checker.

        Returns:
            The collected ``HiveInboundBaseData`` (or subclass) item.

        Raises:
            LookupError: If no ``@hook`` has run in this context, or if
                *data_id* was not part of the hook's type mapping.
        """
        raw = self._var.get()
        item = raw[data_id]
        if data_type is not None:
            return cast(data_type, item)  # type: ignore[valid-type]
        return item

    def _set(self, data: Dict[str, HiveInboundBaseData]) -> contextvars.Token:
        """Store the collected data dict (internal, called by ``@hook``)."""
        return self._var.set(data)

    def _reset(self, token: contextvars.Token) -> None:
        """Restore the previous context value (internal, called by ``@hook``)."""
        self._var.reset(token)


hive_data: _HiveDataAccessor = _HiveDataAccessor()
"""Module-level ``_HiveDataAccessor`` instance.

Import this from your graph node modules to read hook-collected data.
"""


def hive_hook(
    data_ids: Dict[str, Type[HiveInboundBaseData]],
    unique_id_key: str = "unique_id",
) -> Callable:
    """Decorator that pauses a LangGraph node until external data arrives.

    When the decorated node is invoked by LangGraph:

    1. Reads ``state[unique_id_key]`` to get the optional ``unique_id``
       filter.  If the key is absent, ``unique_id`` is ``None`` and all
       inbound items with a matching ``data_id`` qualify.
    2. Calls ``AsyncBuffer.collect_inbound()`` with the declared
       ``data_id`` keys, blocking until one item per key is available.
    3. Re-validates each collected item from the generic
       ``HiveInboundBaseData`` into its declared subclass type using
       ``model_validate``.
    4. Stores the typed dict in the ``hive_data`` context variable.
    5. Calls the original node function.
    6. Resets the context variable in a ``finally`` block.

    Args:
        data_ids: Mapping of ``data_id`` → ``HiveInboundBaseData``
            subclass.  The keys are the ``data_id`` values to wait for;
            the values are the pydantic model classes to deserialise
            each item into.  Example::

                @hook({"payment": PaymentConfirmation, "label": ShippingLabel})

        unique_id_key: State key used to extract the ``unique_id``
            filter.  Defaults to ``"unique_id"``.  If the key is not
            present in the node's state dict, the constraint is
            skipped (any ``unique_id`` matches).

    Returns:
        A decorator that wraps an async graph node function.
    """

    def decorator(fn: Callable) -> Callable:
        @wraps(fn)
        async def wrapper(state: Dict[str, Any], *args: Any, **kwargs: Any) -> Any:
            unique_id: Optional[str] = state.get(unique_id_key)

            buffer = AsyncBufferHandle.discover()
            collected: Dict[str, HiveInboundBaseData] = await buffer.collect_inbound(
                list(data_ids.keys()), unique_id
            )

            typed: Dict[str, HiveInboundBaseData] = {}
            for key, cls in data_ids.items():
                typed[key] = cls.model_validate(collected[key].model_dump())

            token = hive_data._set(typed)
            try:
                return await fn(state, *args, **kwargs)
            finally:
                hive_data._reset(token)

        return wrapper

    return decorator
