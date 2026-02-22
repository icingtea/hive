"""Data models for inter-agent communication.

This module defines the base schemas for all data that flows through
the hive-hook system:

- **Inbound** data (``HiveInboundBaseData``) arrives from external
  sources via the ``/data`` REST endpoint and is routed to graph nodes
  blocked on ``@hook``.
- **Outbound** data (``HiveOutboundBaseData``) is produced by graph
  nodes via ``send()`` and forwarded to the hive mind for delivery to
  other agents.

All inbound models used in ``@hook`` type mappings must inherit from
``HiveInboundBaseData``.  The base class is configured with
``extra="allow"`` so that subclass fields survive deserialisation
through the generic ``/data`` endpoint.
"""

from enum import StrEnum

from pydantic import BaseModel, ConfigDict, Field
from typing import Dict, Optional


class HiveInboundBaseData(BaseModel):
    """Base model for all inbound data items.

    Every piece of data arriving through the ``/data`` endpoint is
    deserialised as this class (preserving extra fields), then
    re-validated to the correct subclass inside the ``@hook``
    decorator.

    Attributes:
        unique_id: Optional identifier used to correlate data with a
            specific graph execution.  When a ``@hook`` node has a
            ``unique_id`` in its state, only items whose ``unique_id``
            matches will be collected.  If ``None``, the item matches
            any subscriber.
        data_id: Routing key that determines which ``@hook`` subscriber
            receives this item.  Must match one of the keys in the
            ``@hook`` type mapping.
    """

    model_config = ConfigDict(extra="allow")

    unique_id: Optional[str] = Field(default=None)
    data_id: str


class EndpointEnum(StrEnum):
    """Supported endpoint types on a destination agent.

    Used in ``HiveOutboundBaseData`` to specify which endpoint on the
    target agent should receive the payload.

    Members:
        START: The ``/start`` endpoint — triggers a new graph execution.
        DATA: The ``/data`` endpoint — feeds data into an existing
            graph execution's inbound buffer.
    """

    START = "start"
    DATA = "data"
    EXTERNAL = "external"


class HiveOutboundBaseData(BaseModel):
    """Base model for all outbound data items.

    Constructed by the ``send()`` function and placed on the outbound
    queue.  The server's permanent forwarder task drains this queue and
    POSTs each item to the hive mind for routing to the destination
    agent.

    Attributes:
        destination_agent_id: Unique identifier of the target agent
            that should receive this message.
        destination_agent_endpoint: Which endpoint on the target agent
            the payload should be delivered to (``"start"`` or
            ``"data"``).
        payload: Arbitrary JSON-serialisable dictionary containing the
            data to deliver.  For ``EndpointEnum.DATA`` destinations
            this should conform to ``HiveInboundBaseData`` (i.e. include
            ``data_id`` and optionally ``unique_id``).
    """

    model_config = ConfigDict(extra="allow")

    destination_agent_id: str
    destination_agent_endpoint: EndpointEnum
    payload: Dict
