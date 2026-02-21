"""Application agent — binary number decision pipeline.

Takes a binary number from a Streamlit frontend, sends it to three
sub-agents (identity, credit, risk), waits for their responses,
and computes a final boolean decision:

    x = 1 if num_ones > num_zeroes else 0
    y = 1 if num_bits is even      else 0
    decision = x AND y

Run:
    uv run streamlit run application-agent.py
"""

from __future__ import annotations

import threading
from typing import cast, Any, Dict, List, Optional

import os
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
class IdentityAgentResponse(HiveInboundBaseData):
    """Response from the identity agent: count of 1-bits."""

    num_ones: int


class CreditAgentResponse(HiveInboundBaseData):
    """Response from the credit agent: count of 0-bits."""

    num_zeroes: int


class RiskAgentResponse(HiveInboundBaseData):
    """Response from the risk agent: total bit count."""

    num_bits: int


# ── Graph State ──────────────────────────────────────────────────────
class State(TypedDict):
    binary_number: str
    num_ones: Optional[int]
    num_zeroes: Optional[int]
    num_bits: Optional[int]
    x: Optional[int]
    y: Optional[int]
    decision: Optional[int]


# ── Shared results (written by decide, read by Streamlit) ────────────
@st.cache_resource
def _get_results_store() -> tuple:
    """Return a (lock, list) pair that survives Streamlit reruns."""
    return threading.Lock(), []


_results_lock, _results = _get_results_store()


# ── Graph Nodes ──────────────────────────────────────────────────────
async def initiate_check(state: State) -> Dict[str, Any]:
    """Read the binary number from state and fan out to three sub-agents."""
    binary_number = state["binary_number"]

    print(f"[initiate_check] binary number: {binary_number}")

    for agent_id in ("identity_agent", "credit_agent", "risk_agent"):
        await send_to_hive(
            destination_agent_id=agent_id,
            destination_agent_endpoint=EndpointEnum.START,
            payload={"binary_number": binary_number},
        )

    return {"binary_number": binary_number}


@hive_hook(
    {
        "identity": IdentityAgentResponse,
        "credit": CreditAgentResponse,
        "risk": RiskAgentResponse,
    }
)
async def await_responses(state: State) -> Dict[str, Any]:
    """Block until all three sub-agents have replied."""
    identity = hive_data.get("identity", IdentityAgentResponse)
    credit = hive_data.get("credit", CreditAgentResponse)
    risk = hive_data.get("risk", RiskAgentResponse)

    print(f"[await_responses] num_ones={identity.num_ones}")
    print(f"[await_responses] num_zeroes={credit.num_zeroes}")
    print(f"[await_responses] num_bits={risk.num_bits}")

    return {
        "num_ones": identity.num_ones,
        "num_zeroes": credit.num_zeroes,
        "num_bits": risk.num_bits,
    }


async def decide(state: State) -> Dict[str, Any]:
    """Compute the final decision from the sub-agent responses."""
    num_ones = cast(int, state["num_ones"])
    num_zeroes = cast(int, state["num_zeroes"])
    num_bits = cast(int, state["num_bits"])

    x = 1 if num_ones > num_zeroes else 0
    y = 1 if num_bits % 2 == 0 else 0
    decision = x & y

    print(f"[decide] binary_number={state['binary_number']}")
    print(f"[decide] num_ones={num_ones}, num_zeroes={num_zeroes} → x={x}")
    print(f"[decide] num_bits={num_bits} ({'even' if y else 'odd'}) → y={y}")
    print(f"[decide] decision = x AND y = {decision}")

    result = {
        "binary_number": state["binary_number"],
        "num_ones": num_ones,
        "num_zeroes": num_zeroes,
        "num_bits": num_bits,
        "x": x,
        "y": y,
        "decision": decision,
    }
    with _results_lock:
        _results.append(result)

    return {"x": x, "y": y, "decision": decision}


# ── Compile Graph ────────────────────────────────────────────────────
graph = StateGraph(State)
graph.add_node("initiate_check", initiate_check)
graph.add_node("await_responses", await_responses)
graph.add_node("decide", decide)
graph.set_entry_point("initiate_check")
graph.add_edge("initiate_check", "await_responses")
graph.add_edge("await_responses", "decide")
graph.add_edge("decide", END)

compiled = graph.compile()

# ── Backend (FastAPI in a background thread) ─────────────────────────
BASE_URL = f"http://localhost:{os.getenv('HIVE_POD_PORT')}"


@st.cache_resource
def _boot_backend():
    """Start the FastAPI/uvicorn server once, in a daemon thread."""
    t = threading.Thread(
        target=start_server,
        args=(compiled,),
        kwargs={"agent_id": "application_agent"},
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
st.set_page_config(page_title="application-agent", page_icon="🧠", layout="wide")

st.title("🧠 Application Agent — Binary Decision Pipeline")
st.caption(
    "Enter a binary number → dispatched to identity, credit & risk agents → final decision"
)

if "log" not in st.session_state:
    st.session_state.log = []

# ── Sidebar: controls ────────────────────────────────────────────────
with st.sidebar:
    st.header("Controls")

    binary_input = st.text_input("Binary number (≥4 bits)", value="1001", max_chars=32)

    if st.button("🚀 Run Pipeline", use_container_width=True, type="primary"):
        stripped = binary_input.strip()
        if len(stripped) < 4 or not all(c in "01" for c in stripped):
            st.error("Please enter a valid binary string of at least 4 bits.")
        else:
            resp = _post("/start", {"binary_number": stripped})
            st.session_state.log.append(("start", stripped, resp))
            st.toast(f"Pipeline started with **{stripped}**!", icon="🚀")

    st.divider()
    st.subheader("Send Agent Responses")

    tab_id, tab_cr, tab_ri = st.tabs(["🆔 Identity", "💳 Credit", "⚠️ Risk"])

    with tab_id:
        num_ones = st.number_input("num_ones", value=0, step=1, min_value=0)
        if st.button("Send Identity Response", use_container_width=True):
            payload = {"data_id": "identity", "num_ones": num_ones}
            resp = _post("/data", payload)
            st.session_state.log.append(("identity", str(num_ones), resp))
            st.toast("Identity response sent!", icon="🆔")

    with tab_cr:
        num_zeroes = st.number_input("num_zeroes", value=0, step=1, min_value=0)
        if st.button("Send Credit Response", use_container_width=True):
            payload = {"data_id": "credit", "num_zeroes": num_zeroes}
            resp = _post("/data", payload)
            st.session_state.log.append(("credit", str(num_zeroes), resp))
            st.toast("Credit response sent!", icon="💳")

    with tab_ri:
        num_bits = st.number_input("num_bits", value=0, step=1, min_value=0)
        if st.button("Send Risk Response", use_container_width=True):
            payload = {"data_id": "risk", "num_bits": num_bits}
            resp = _post("/data", payload)
            st.session_state.log.append(("risk", str(num_bits), resp))
            st.toast("Risk response sent!", icon="⚠️")

# ── Main area: decision results (auto-refreshes every 2s) ────────────
@st.fragment(run_every=1)
def _decision_results() -> None:
    st.subheader("� Decision Results")

    with _results_lock:
        snapshot = list(_results)

    if not snapshot:
        st.info("Waiting for pipeline to complete…")
    else:
        for result in reversed(snapshot):
            d = result["decision"]
            icon = "✅" if d else "❌"
            with st.expander(
                f"{icon} `{result['binary_number']}` → decision = **{d}**",
                expanded=True,
            ):
                c1, c2, c3 = st.columns(3)
                c1.metric("num_ones", result["num_ones"])
                c2.metric("num_zeroes", result["num_zeroes"])
                c3.metric("num_bits", result["num_bits"])

                c4, c5, c6 = st.columns(3)
                c4.metric("x (ones > zeroes)", result["x"])
                c5.metric("y (bits even)", result["y"])
                c6.metric("decision (x AND y)", result["decision"])


_decision_results()

st.divider()

# ── Event log ────────────────────────────────────────────────────────
st.subheader("📋 Event Log")

if not st.session_state.log:
    st.info("No events yet — enter a binary number and run the pipeline.")
else:
    for i, (kind, value, resp) in enumerate(reversed(st.session_state.log)):
        icon = {
            "start": "🚀",
            "identity": "🆔",
            "credit": "💳",
            "risk": "⚠️",
        }.get(kind, "📌")
        error = resp.get("error")
        if error:
            st.error(f"{icon} **{kind}** (`{value}`) — ❌ `{error}`")
        else:
            st.success(f"{icon} **{kind}** (`{value}`) — ✅ `{resp}`")

# ── Footer ───────────────────────────────────────────────────────────
st.divider()
st.caption("Pipeline: initiate_check → await_responses → decide")
