"""Streamlit frontend for the order-processing pipeline.

A 4-node LangGraph pipeline launched from a Streamlit dashboard:
  1. validate_order    — checks the order, sends notification outbound
  2. await_payment     — @hook blocks until a PaymentConfirmation arrives
  3. await_fulfillment — @hook blocks until ShippingLabel AND WarehouseAck arrive
  4. complete_order    — marks the order as done

Run (2 terminals):
    # Terminal 1 — mock hive mind
    uv run python test_hive_mind.py

    # Terminal 2 — streamlit app (auto-starts the FastAPI backend)
    uv run streamlit run test.py
"""

from __future__ import annotations

import asyncio
import operator
import threading
from typing import Annotated, Any, Dict, List, Optional

import httpx
import streamlit as st
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


# ── Data Models ──────────────────────────────────────────────────────
class PaymentConfirmation(HiveInboundBaseData):
    amount: float
    currency: str


class ShippingLabel(HiveInboundBaseData):
    carrier: str
    tracking_number: str


class WarehouseAck(HiveInboundBaseData):
    warehouse_id: str


# ── Graph State ──────────────────────────────────────────────────────
class State(TypedDict):
    unique_id: Optional[str]
    messages: Annotated[List[str], operator.add]


# ── Graph Nodes ──────────────────────────────────────────────────────
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


# ── Compile Graph ────────────────────────────────────────────────────
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

# ── Backend (FastAPI in a background thread) ─────────────────────────
BASE_URL = "http://localhost:6969"


@st.cache_resource
def _boot_backend():
    """Start the FastAPI/uvicorn server once, in a daemon thread."""
    t = threading.Thread(
        target=start_server,
        args=(compiled,),
        kwargs={"agent_id": "pod1"},
        daemon=True,
    )
    t.start()
    return t


_boot_backend()


# ── Helpers ──────────────────────────────────────────────────────────
def _post(endpoint: str, payload: Dict[str, Any]) -> Dict[str, Any]:
    """Fire-and-forget POST to the local FastAPI backend."""
    try:
        r = httpx.post(f"{BASE_URL}{endpoint}", json=payload, timeout=5.0)
        return r.json()
    except Exception as e:
        return {"error": str(e)}


# ── Streamlit UI ─────────────────────────────────────────────────────
st.set_page_config(page_title="hive-hook", page_icon="🐝", layout="wide")

st.title("🐝 Hive-Hook Order Pipeline")
st.caption("Streamlit dashboard for the LangGraph order-processing demo")

if "log" not in st.session_state:
    st.session_state.log = []

# ── Sidebar: controls ────────────────────────────────────────────────
with st.sidebar:
    st.header("Controls")

    unique_id = st.text_input("Order ID (unique_id)", value="order_001")

    st.divider()

    # Start order
    if st.button("🚀 Start Order", use_container_width=True, type="primary"):
        resp = _post("/start", {"unique_id": unique_id, "messages": []})
        st.session_state.log.append(("start", unique_id, resp))
        st.toast(f"Order **{unique_id}** started!", icon="🚀")

    st.divider()
    st.subheader("Send Data")

    tab_pay, tab_ship, tab_wh = st.tabs(["💳 Payment", "📦 Shipping", "🏭 Warehouse"])

    with tab_pay:
        amount = st.number_input("Amount", value=49.99, step=0.01, format="%.2f")
        currency = st.text_input("Currency", value="USD")
        if st.button("Send Payment", use_container_width=True):
            payload = {
                "data_id": "payment",
                "unique_id": unique_id,
                "amount": amount,
                "currency": currency,
            }
            resp = _post("/data", payload)
            st.session_state.log.append(("payment", unique_id, resp))
            st.toast("Payment sent!", icon="💳")

    with tab_ship:
        carrier = st.text_input("Carrier", value="FedEx")
        tracking = st.text_input("Tracking Number", value="FX123456")
        if st.button("Send Shipping Label", use_container_width=True):
            payload = {
                "data_id": "shipping_label",
                "unique_id": unique_id,
                "carrier": carrier,
                "tracking_number": tracking,
            }
            resp = _post("/data", payload)
            st.session_state.log.append(("shipping_label", unique_id, resp))
            st.toast("Shipping label sent!", icon="📦")

    with tab_wh:
        warehouse_id = st.text_input("Warehouse ID", value="WH-EAST-07")
        if st.button("Send Warehouse Ack", use_container_width=True):
            payload = {
                "data_id": "warehouse_ack",
                "unique_id": unique_id,
                "warehouse_id": warehouse_id,
            }
            resp = _post("/data", payload)
            st.session_state.log.append(("warehouse_ack", unique_id, resp))
            st.toast("Warehouse ack sent!", icon="🏭")

# ── Main area: event log ─────────────────────────────────────────────
st.subheader("📋 Event Log")

if not st.session_state.log:
    st.info("No events yet — start an order from the sidebar.")
else:
    for i, (kind, uid, resp) in enumerate(reversed(st.session_state.log)):
        icon = {
            "start": "🚀",
            "payment": "💳",
            "shipping_label": "📦",
            "warehouse_ack": "🏭",
        }.get(kind, "📌")
        error = resp.get("error")
        if error:
            st.error(f"{icon} **{kind}** (order `{uid}`) — ❌ `{error}`")
        else:
            st.success(f"{icon} **{kind}** (order `{uid}`) — ✅ `{resp}`")

# ── Footer ───────────────────────────────────────────────────────────
st.divider()
st.caption(
    "Pipeline: validate_order → await_payment → await_fulfillment → complete_order"
)
