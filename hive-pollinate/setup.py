from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, IPvAnyAddress, AnyUrl
import paramiko

app = FastAPI()

class SetupRequest(BaseModel):
    ip: IPvAnyAddress
    username: str
    password: str
    port: int = 2222
    git: AnyUrl
    pyfile: str
    workerid: str


def run_ssh(ip: str, username: str, password: str, pyfile: str, port=2222):
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())

    try:
        client.connect(
            hostname=str(ip),
            username=username,
            password=password,
            timeout=10,
            banner_timeout=10,
            auth_timeout=10,
            port=port,
        )

        sftp = client.open_sftp()
        sftp.put("setup.sh", "/tmp/setup.sh")
        sftp.put("hive_panopticon", "/tmp/hive_panopticon")
        sftp.close()

        bootstrap_cmd = "chmod +x /tmp/setup.sh && sudo bash /tmp/setup.sh"
        stdin, stdout, stderr = client.exec_command(bootstrap_cmd)

        out = stdout.read().decode()
        err = stderr.read().decode()

        return out, err

    finally:
        client.close()


@app.post("/api/bootstrap")
def bootstrap(req: SetupRequest):
    try:
        out, err = run_ssh(
            ip=str(req.ip),
            username=req.username,
            password=req.password,
            port=req.port,
            pyfile=req.pyfile,
        )
        return {"ok": True, "stdout": out, "stderr": err}

    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))
