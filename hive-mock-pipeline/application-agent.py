"""Application agent — binary number decision pipeline.

Generates a random binary number (4–8 bits), sends it to three
sub-agents (identity, credit, risk), waits for their responses,
and computes a final boolean decision:

    x = 1 if num_ones > num_zeroes else 0
    y = 1 if num_bits is even      else 0
    decision = x AND y

Run:
    uv run python application-agent.py
"""

from __future__ import annotations

import random
from typing import cast, Any, Dict, Optional

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


# ── Graph Nodes ──────────────────────────────────────────────────────
async def initiate_check(state: State) -> Dict[str, Any]:
    """Generate a binary number and fan out to three sub-agents."""
    n_bits = random.randint(4, 8)
    binary_number = "".join(random.choice("01") for _ in range(n_bits))

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

# ── Run ──────────────────────────────────────────────────────────────
if __name__ == "__main__":
    start_server(compiled, agent_id="application_agent")
