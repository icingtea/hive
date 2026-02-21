"""Deterministic order-processing service demo.

A 4-node LangGraph pipeline:
  1. validate_order    — checks the order, sends notification outbound
  2. await_payment     — @hook blocks until a PaymentConfirmation arrives,
                         then sends payment receipt outbound
  3. await_fulfillment — @hook blocks until ShippingLabel AND WarehouseAck arrive,
                         then sends tracking info outbound
  4. complete_order    — marks the order as done, sends completion outbound

Run (3 terminals):
    # Terminal 1 — mock hive mind
    uv run python test_hive_mind.py

    # Terminal 2 — agent server
    uv run python test.py

    # Terminal 3 — trigger the flow
    curl -X POST http://localhost:6969/start \
      -H "Content-Type: application/json" \
      -d '{"unique_id": "order_001", "messages": []}'

    curl -X POST http://localhost:6969/data \
      -H "Content-Type: application/json" \
      -d '{"data_id": "payment", "unique_id": "order_001", "amount": 49.99, "currency": "USD"}'

    curl -X POST http://localhost:6969/data \
      -H "Content-Type: application/json" \
      -d '{"data_id": "shipping_label", "unique_id": "order_001", "carrier": "FedEx", "tracking_number": "FX123456"}'

    curl -X POST http://localhost:6969/data \
      -H "Content-Type: application/json" \
      -d '{"data_id": "warehouse_ack", "unique_id": "order_001", "warehouse_id": "WH-EAST-07"}'
"""

from __future__ import annotations

import asyncio
import operator
from typing import Annotated, Any, Dict, List, Optional

from langgraph.graph import END, StateGraph
from typing_extensions import TypedDict

from hive_hook import (
    EndpointEnum,
    HiveInboundBaseData,
    hive_data,
    hive_hook,
    send_to_hive,
    start_server,
)


class PaymentConfirmation(HiveInboundBaseData):
    amount: float
    currency: str


class ShippingLabel(HiveInboundBaseData):
    carrier: str
    tracking_number: str


class WarehouseAck(HiveInboundBaseData):
    warehouse_id: str


class State(TypedDict):
    unique_id: Optional[str]
    messages: Annotated[List[str], operator.add]


async def validate_order(state: State) -> Dict[str, Any]:
    uid = state.get("unique_id", "unknown")
    print(f"[validate_order] validating {uid}")
    await asyncio.sleep(0.5)
    print(f"[validate_order] order {uid} is valid")

    await send_to_hive(
        destination_agent_id="billing_agent",
        destination_agent_endpoint=EndpointEnum.DATA,
        payload={"event": "order_validated", "order_id": uid},
    )

    return {"messages": [f"order {uid} validated"]}


@hive_hook({"payment": PaymentConfirmation})
async def await_payment(state: State) -> Dict[str, Any]:
    payment = hive_data.get("payment", PaymentConfirmation)
    print(f"[await_payment] received {payment.amount} {payment.currency}")

    await send_to_hive(
        destination_agent_id="receipt_agent",
        destination_agent_endpoint=EndpointEnum.DATA,
        payload={
            "event": "payment_received",
            "amount": payment.amount,
            "currency": payment.currency,
        },
    )

    return {"messages": [f"paid {payment.amount} {payment.currency}"]}


@hive_hook({"shipping_label": ShippingLabel, "warehouse_ack": WarehouseAck})
async def await_fulfillment(state: State) -> Dict[str, Any]:
    label = hive_data.get("shipping_label", ShippingLabel)
    ack = hive_data.get("warehouse_ack", WarehouseAck)
    print(f"[await_fulfillment] label: {label.carrier} {label.tracking_number}")
    print(f"[await_fulfillment] warehouse: {ack.warehouse_id}")

    await send_to_hive(
        destination_agent_id="tracking_agent",
        destination_agent_endpoint=EndpointEnum.START,
        payload={
            "event": "shipment_ready",
            "carrier": label.carrier,
            "tracking_number": label.tracking_number,
            "warehouse": ack.warehouse_id,
        },
    )

    return {
        "messages": [f"shipped via {label.carrier}", f"packed at {ack.warehouse_id}"]
    }


async def complete_order(state: State) -> Dict[str, Any]:
    uid = state.get("unique_id", "unknown")
    print(f"[complete_order] order {uid} complete — {state['messages']}")

    await send_to_hive(
        destination_agent_id="notification_agent",
        destination_agent_endpoint=EndpointEnum.DATA,
        payload={"event": "order_complete", "order_id": uid},
    )

    return {"messages": ["done"]}


graph = StateGraph(State)
graph.add_node("validate_order", validate_order)
graph.add_node("await_payment", await_payment)
graph.add_node("await_fulfillment", await_fulfillment)
graph.add_node("complete_order", complete_order)
graph.set_entry_point("validate_order")
graph.add_edge("validate_order", "await_payment")
graph.add_edge("await_payment", "await_fulfillment")
graph.add_edge("await_fulfillment", "complete_order")
graph.add_edge("complete_order", END)

compiled = graph.compile()
start_server(compiled, agent_id="pod1")
