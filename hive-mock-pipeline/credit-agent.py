from __future__ import annotations

import asyncio
import random
from typing import Any, Dict

from langgraph.graph import END, StateGraph
from typing_extensions import TypedDict

from hive_hook import (
    EndpointEnum,
    send_to_hive,
    start_server,
)

class State(TypedDict):
    binary_number: str


@hive_hook({"binary": BinaryData})
async def process(state: State) -> Dict[str, Any]:
    binary_number = state["binary_number"]
    delay = random.randint(10, 20)
    await asyncio.sleep(delay)

    
    num_zeroes = data.binary.count("0")

    await send_to_hive(
        destination_agent_id="application_agent",
        destination_agent_endpoint=EndpointEnum.DATA,
        payload={
            "data_id": "credit",
            "num_zeroes": num_zeroes,
        },
    )

    return {}

graph = StateGraph(State)
graph.add_node("process", process)
graph.set_entry_point("process")
graph.add_edge("process", END)

compiled = graph.compile()
start_server(compiled, agent_id = "credit_agent")



