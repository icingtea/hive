from fastapi import FastAPI
from pydantic import BaseModel
from enum import Enum
import httpx

app = FastAPI()
ORCHESTRATOR_ADDRESS = "http://orchestrator:8000"

class ManageOption(str, Enum):
    PAUSE = "PAUSE"
    KILL = "KILL"
    RESTART = "RESTART"

class InitRequest(BaseModel):
    git_url: str
    pod_id: str

class ManageRequest(BaseModel):
    option: ManageOption
    pod_id: str


@app.post("/proxy/init")
async def init(req: InitRequest):
    async with httpx.AsyncClient() as client:
        resp = await client.post(
            f"{ORCHESTRATOR_ADDRESS}/init",
            json=req.model_dump()
        )
    return resp.json()


@app.post("/proxy/manage")
async def manage(req: ManageRequest):
    async with httpx.AsyncClient() as client:
        resp = await client.post(
            f"{ORCHESTRATOR_ADDRESS}/manage",
            json=req.model_dump()
        )
    return resp.json()
