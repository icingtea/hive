"""Mock hive mind server.

Listens on port 9000 and prints everything received at /api/v1/communicate.

Run:
    uv run python test_hive_mind.py
"""

from __future__ import annotations

import json
from typing import Any, Dict

import uvicorn
from fastapi import FastAPI, Request

app = FastAPI(title="mock-hive-mind-idk")


@app.post("/pod")
async def communicate(request: Request) -> Dict[str, str]:
    body = await request.json()
    print("[HIVE MIND] Received outbound message:")
    print(json.dumps(body, indent=2))
    print()
    return {"status": "ok"}


if __name__ == "__main__":
    uvicorn.run(app, host="127.0.0.1", port=9000)
