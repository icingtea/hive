# 🐝 hive

**Hive** is a distributed multi-agent orchestration framework that lets you run LangGraph agents in parallel across a self-managed Kubernetes cluster — with minimal code changes.

At its core, hive solves the hardest part of multi-agent systems: **inter-agent communication and synchronization**. Agents can declare exactly what data they need before proceeding, block execution until that data arrives from other agents or external sources, and forward results onward — all transparently, through a central orchestrator.

---

## How It Works

```
┌─────────────────────────────────────────────────────────────┐
│                        hive-orchestrator                    │
│         (Go · Kubernetes · SQLite · REST Dashboard)         │
│                                                             │
│  /api/v1/communicate   →   routes messages between agents   │
│  /api/v1/message/:id   →   external ingress                 │
│  /api/v1/result/:id    →   poll for agent output            │
│  /dashboard            →   live web UI                      │
└────────────────┬───────────────────────┬────────────────────┘
                 │                       │
        kubectl exec forwarding   agent registration
                 │                       │
     ┌───────────▼───────┐   ┌───────────▼───────┐
     │   Agent Pod A     │   │   Agent Pod B     │
     │  ┌─────────────┐  │   │  ┌─────────────┐  │
     │  │  hive-hook  │  │   │  │  hive-hook  │  │
     │  │  FastAPI    │  │   │  │  FastAPI    │  │
     │  │  LangGraph  │  │   │  │  LangGraph  │  │
     │  └─────────────┘  │   │  └─────────────┘  │
     │  ┌─────────────┐  │   │  ┌─────────────┐  │
     │  │  panopticon │  │   │  │  panopticon │  │
     │  │(Zig sidecar)│  │   │  │(Zig sidecar)│  │
     │  └─────────────┘  │   │  └─────────────┘  │
     └───────────────────┘   └───────────────────┘
```

### The Core Idea

Each agent is a LangGraph graph running inside a pod. Nodes within the graph can be decorated with `@hive_hook`, which causes the node to **pause execution** until the required data arrives from another agent. When data is ready, execution resumes automatically — the hook re-validates the incoming payload into typed Pydantic models and makes it available inside the node.

This allows genuinely parallel pipelines: Agent A can trigger Agents B, C, and D simultaneously, and Agent E can wait on all three before proceeding — without any polling, timeouts, or manual coordination.

---

## Repository Structure

```
hive/
├── hive-hook/              # Python SDK — @hive_hook decorator + FastAPI server
├── hive-orchestrator/      # Go orchestrator — Kubernetes management + routing
├── hive-panopticon/        # Zig sidecar — heartbeat telemetry per pod
├── hive-pollinate/         # Node bootstrapper — SSH into servers, joins k8s cluster
├── hive-mock-pipeline/     # Example agent submodules
└── README.md
```

---

## Components

### `hive-hook` — Python Agent SDK

The Python library that every agent imports. Install it with:

```bash
pip install hive-hook
```

It provides:

- **`@hive_hook`** — decorator that pauses a LangGraph node until declared data IDs arrive
- **`hive_data`** — typed context accessor to read collected data inside a hooked node
- **`send_to_hive()`** — coroutine to forward data or trigger other agents
- **`start_server()`** — launches the agent's FastAPI server (wires up the buffer, forwarder, and endpoints)

#### Writing an Agent

```python
from hive_hook import (
    HiveInboundBaseData, EndpointEnum,
    hive_data, hive_hook, send_to_hive, start_server,
)
from langgraph.graph import StateGraph, END
from typing import Optional, Annotated, List
import operator

# 1. Define typed data models for what this agent receives
class CreditDecision(HiveInboundBaseData):
    approved: bool
    score: int
    reason: str

# 2. Define your graph state
class State(TypedDict):
    unique_id: Optional[str]
    messages: Annotated[List[str], operator.add]

# 3. Pause at a node until "credit_decision" arrives from another agent
@hive_hook({"credit_decision": CreditDecision})
async def await_credit(state: State):
    decision = hive_data.get("credit_decision", CreditDecision)
    if decision.approved:
        await send_to_hive(
            destination_agent_id="offer-agent",
            destination_agent_endpoint=EndpointEnum.START,
            payload={"unique_id": state["unique_id"], "score": decision.score},
        )
    return {"messages": [f"credit: {decision.reason}"]}

# 4. Build and run your graph
graph = StateGraph(State)
graph.add_node("await_credit", await_credit)
# ... add more nodes and edges ...
compiled = graph.compile()

start_server(compiled, agent_id="underwriting-agent")
```

#### `@hive_hook` in Detail

```python
@hive_hook(
    data_ids={"payment": PaymentConfirmation, "label": ShippingLabel},
    unique_id_key="unique_id",   # state key used to isolate concurrent runs
)
async def my_node(state: dict) -> dict:
    payment = hive_data.get("payment", PaymentConfirmation)
    label   = hive_data.get("label",   ShippingLabel)
    ...
```

- **`data_ids`** — maps data ID strings to Pydantic model classes. The node blocks until one item for each key arrives.
- **`unique_id_key`** — when set, only items whose `unique_id` matches `state[unique_id_key]` are collected. This isolates concurrent graph executions from each other.

#### Agent Endpoints

Every agent automatically exposes three HTTP endpoints:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/id` | Returns this agent's declared ID |
| `POST` | `/start` | Starts a new graph execution with optional initial state |
| `POST` | `/data` | Delivers a data item into the inbound buffer |

#### `send_to_hive()` — Sending to Other Agents

```python
await send_to_hive(
    destination_agent_id="risk-agent",
    destination_agent_endpoint=EndpointEnum.DATA,   # or START, EXTERNAL
    payload={
        "data_id": "risk_result",
        "unique_id": state["unique_id"],
        "score": 720,
    },
)
```

| Endpoint | Effect |
|----------|--------|
| `EndpointEnum.START` | Triggers a new graph execution on the target agent |
| `EndpointEnum.DATA` | Delivers data into an existing execution |
| `EndpointEnum.EXTERNAL` | Sends a final result back to the orchestrator for external polling |

---

### `hive-orchestrator` — Central Orchestrator

A Go service that manages the full agent lifecycle on Kubernetes.

#### Key Responsibilities

- **Agent Registry** — stores agent definitions (repo URL, branch) in SQLite
- **Image Building** — clones GitHub repos and builds Docker images via Kaniko (no Docker daemon required)
- **Pod Spawning** — deploys built images as Kubernetes pods with the Zig sidecar
- **Message Routing** — receives outbound messages from agents and forwards them to the correct destination pod via `kubectl exec`
- **Agent Discovery** — probes each running pod's `/id` endpoint and maintains a name-to-pod routing table
- **External Ingress** — accepts messages and start triggers from outside the cluster
- **Result Polling** — stores `EXTERNAL` endpoint results for retrieval by external services
- **Web Dashboard** — live SSE-powered UI for deployments, logs, and communications

#### Running the Orchestrator

```bash
cd hive-orchestrator
cp hive.yaml.example hive.yaml   # configure k8s, registry, server settings
go run ./cmd/orchestrator
```

Default config (`hive.yaml`):

```yaml
server:
  host: 0.0.0.0
  port: 8181

kubernetes:
  kubeconfig: ~/.kube/config
  namespace: hive-agents

registry:
  url: hive-registry:5000
```

#### REST API

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/agents` | Register a new agent (repo URL + name) |
| `POST` | `/api/v1/agents/:id/deploy` | Deploy an agent (triggers build + spawn) |
| `GET` | `/api/v1/deployments/:id` | Get deployment status |
| `POST` | `/api/v1/communicate` | Route a message between agents |
| `POST` | `/api/v1/message/:agent_name` | Send start/data from an external source |
| `GET` | `/api/v1/result/:agent_name` | Poll for a result pushed via `EXTERNAL` endpoint |
| `GET` | `/api/v1/commlogs` | Get all routed messages |
| `GET` | `/dashboard` | Web dashboard |

#### Deploy an Agent via API

```bash
# 1. Register the agent
curl -X POST http://localhost:8181/api/v1/agents \
  -H "Content-Type: application/json" \
  -d '{"name": "risk-agent", "repo_url": "https://github.com/org/risk-agent", "branch": "main"}'

# 2. Deploy it (builds image + spawns pod)
curl -X POST http://localhost:8181/api/v1/agents/<agent-id>/deploy

# 3. Trigger it from outside
curl -X POST http://localhost:8181/api/v1/message/risk-agent \
  -H "Content-Type: application/json" \
  -d '{"type": "start", "payload": {"unique_id": "app-001", "messages": []}}'

# 4. Poll for result
curl http://localhost:8181/api/v1/result/risk-agent
```

#### Agent Repo Requirements

Repos deployed through the orchestrator must have at the root:
- At least one `.py` file (the entry point)
- `requirements.txt`

The orchestrator auto-generates a Dockerfile, installs `hive-hook` alongside your dependencies, and builds the image via Kaniko.

---

### `hive-pollinate` — Node Bootstrapper

A FastAPI service that automates adding bare servers to the Kubernetes cluster. Given SSH credentials for a server, it:

1. SSHes into the machine
2. Installs Docker, Go, and k3s agent
3. Configures the registry mirror
4. Joins the server to the existing k3s cluster

```bash
curl -X POST http://localhost:8000/api/bootstrap \
  -H "Content-Type: application/json" \
  -d '{
    "ip": "203.0.113.42",
    "username": "root",
    "password": "...",
    "port": 22,
    "git": "https://github.com/org/agent-repo",
    "pyfile": "agent.py"
  }'
```

This lets you expand the cluster horizontally — any new server can become a scheduling target for agent pods.

---

### `hive-panopticon` — Zig Sidecar

A lightweight Zig binary that runs as a sidecar container alongside every agent pod. Every 5 seconds it collects system telemetry and POSTs it to the orchestrator:

- Agent PID
- Memory usage (VmRSS from `/proc/<pid>/status`)
- Memory limit (from cgroup `memory.max`)
- Kernel version
- System uptime

This telemetry is displayed live in the dashboard.

---

## Example Pipeline

Below is a sketch of a loan application pipeline where four agents run in parallel, each waiting on specific data before proceeding:

```
external request
      │
      ▼
application-agent ──► identity-agent   (verifies identity)
                  ──► credit-agent     (pulls credit score)
                  ──► risk-agent       (assesses risk)
                         │ (all three send results back)
                         ▼
                  underwriting-agent   (@hive_hook waits for all three)
                         │
                         ▼
                  decision (EXTERNAL endpoint → caller polls result)
```

Each agent is a separate GitHub repo deployed independently. They communicate only through `send_to_hive()` calls. The orchestrator routes everything.

---

## Environment Variables (Agent Pods)

These are injected automatically by the orchestrator when spawning a pod:

| Variable | Description |
|----------|-------------|
| `HIVE_POD_PORT` | Port for the agent's FastAPI server (default `8080`) |
| `HIVE_MIND_ADDRESS` | Base URL of the orchestrator |
| `HIVE_MIND_COMMUNICATION_ENDPOINT` | Endpoint for outbound messages (`api/v1/communicate`) |
| `HIVE_DEPLOYMENT_ID` | This deployment's ID |
| `HIVE_AGENT_ID` | This agent's ID |
| `HIVE_POD_NAME` | Kubernetes pod name |

For local development, create a `.env` file in your agent directory:

```env
HIVE_POD_PORT=6969
HIVE_MIND_ADDRESS=http://localhost:9000
HIVE_MIND_COMMUNICATION_ENDPOINT=api/v1/communicate
```

---

## Local Development

### Running the Mock Pipeline (no Kubernetes required)

```bash
# Terminal 1 — start a mock hive mind
cd hive-hook
uv run python test_hive_mind.py

# Terminal 2 — start the Streamlit demo agent
uv run streamlit run test.py
```

The demo runs a 4-node order-processing pipeline:
`validate_order → await_payment → await_fulfillment → complete_order`

Use the sidebar to start orders and inject payment/shipping/warehouse data to watch the graph proceed step by step.

### Running the Orchestrator Locally

The orchestrator gracefully falls back to a no-op pod manager if Kubernetes is unavailable, so you can develop against it without a cluster:

```bash
cd hive-orchestrator
go run ./cmd/orchestrator
# Dashboard at http://localhost:8181/dashboard
```

---

## Architecture Notes

**Why `kubectl exec` for forwarding?**
The orchestrator doesn't need direct network access to pod IPs. It forwards messages by exec-ing a Python one-liner inside the target pod's `agent` container. This works identically in local kind clusters and in remote production clusters.

**Why Kaniko for builds?**
Kaniko builds Docker images from inside a Kubernetes pod without needing a Docker daemon or privileged access. The orchestrator creates a short-lived Kaniko pod per build, streams the logs to the dashboard, and cleans up afterwards.

**Concurrency isolation via `unique_id`**
Multiple graph executions can run in the same agent process simultaneously. The `unique_id` field in both state and data payloads ensures that `@hive_hook` nodes only consume data intended for their specific run — preventing cross-contamination between parallel pipelines.

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Agent SDK | Python, FastAPI, LangGraph, Pydantic |
| Orchestrator | Go, chi, client-go, SQLite |
| Build system | Kaniko (in-cluster image builds) |
| Runtime | Kubernetes (k3s) |
| Sidecar | Zig |
| Node provisioning | SSH + k3s agent installer |
| Dashboard | HTMX, SSE, vanilla CSS |