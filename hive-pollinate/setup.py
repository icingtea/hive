from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, IPvAnyAddress, AnyUrl
import paramiko

app = FastAPI()

print("[BOOT] setup.py loaded")

class SetupRequest(BaseModel):
    ip: IPvAnyAddress
    username: str
    password: str
    port: int = 22
    git: AnyUrl
    pyfile: str

def run_ssh(ip: str, username: str, password: str, pyfile: str, port=22):
    print("[SSH] starting run_ssh()")
    print(f"[SSH] target = {ip}:{port} user={username}")

    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())

    try:
        print("[SSH] connecting...")
        client.connect(
            hostname=str(ip),
            username=username,
            password=password,
            timeout=10,
            banner_timeout=10,
            auth_timeout=10,
            port=port,
        )
        print("[SSH] connected OK")

        print("[SSH] opening sftp...")
        sftp = client.open_sftp()
        print("[SSH] uploading setup.sh...")
        sftp.put("setup.sh", "/tmp/setup.sh")
        sftp.close()
        print("[SSH] upload complete")

        bootstrap_cmd = "chmod +x /tmp/setup.sh && bash /tmp/setup.sh"
        print(f"[SSH] executing: {bootstrap_cmd}")

        stdin, stdout, stderr = client.exec_command(bootstrap_cmd)

        print("[SSH] reading stdout...")
        out = stdout.read().decode()
        print("[SSH] reading stderr...")
        err = stderr.read().decode()

        print("[SSH] command finished")
        return out, err

    finally:
        print("[SSH] closing client")
        client.close()


@app.post("/api/bootstrap")
def bootstrap(req: SetupRequest):
    print("[API] /api/bootstrap HIT")
    print(f"[API] payload ip={req.ip} user={req.username} port={req.port}")

    try:
        out, err = run_ssh(
            ip=str(req.ip),
            username=req.username,
            password=req.password,
            port=req.port,
            pyfile=req.pyfile,
        )

        print("[API] returning response")
        return {"ok": True, "stdout": out, "stderr": err}

    except Exception as e:
        print("[API] ERROR:", str(e))
        raise HTTPException(status_code=500, detail=str(e))
