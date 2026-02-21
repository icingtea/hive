from __future__ import annotations
import random

from hive_hook import start_server, send_to_hive, EndpointEnum, hive_hook, hive_data, HiveInboundBaseData
from langgraph.graph import StateGraph, END
from typing_extensions import TypedDict
from typing import Dict, Any

class State(TypedDict):
    binary_number: str


async def process(state: State) -> Dict[str, Any]:
    binary_number = state['binary_number']
    
    delay = random.randint(10, 20)
    await asyncio.sleep(delay)

    data = hive_data.get("binary", BinaryData)
    num_ones = data.binary.count("1")

    await send_to_hive(
        destination_agent_id="application_agent",
        destination_agent_endpoint=EndpointEnum.DATA,
        payload={
            "data_id": "identity",
            "num_ones": num_ones,
        },
    )

    return {}

graph = StateGraph(State)
graph.add_node("process", process)
graph.set_entry_point("process")
graph.add_edge("process", END)

compiled = graph.compile()
start_server(compiled, agent_id = "identity_agent")


