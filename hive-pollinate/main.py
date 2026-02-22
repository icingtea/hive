from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, IPvAnyAddress, AnyUrl, TypeAdapter
import paramiko

app = FastAPI()

class HostRequest(BaseModel):
    ip: IPvAnyAddress
    username: str
    password: str
    port: int
    git: AnyUrl
    pyfile: str

def run_ssh(ip: str, username: str, password: str, git: str, pyfile: str, port=2222):
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
        sftp.put("dockerfile", "/tmp/Dockerfile")
        sftp.put("hive_panopticon", "/tmp/hive_panopticon")
        sftp.close()

        bootstrap_cmd = "chmod +x /tmp/setup.sh && sudo bash /tmp/setup.sh"
        client.exec_command(bootstrap_cmd)

        docker_build = f"""
            docker build -t bootstrap-img \
              --build-arg GIT_URL='{git}' \
              --build-arg PYFILE='{pyfile}' \
              -f /tmp/Dockerfile /tmp
        """

        stdin, stdout, stderr = client.exec_command(docker_build)

        out = stdout.read().decode()
        err = stderr.read().decode()

        return out, err

    finally:
        client.close()

@app.post("/api/bootstrap")
def bootstrap(req: HostRequest):
    try:
        out, err = run_ssh(
            ip=str(req.ip),
            username=req.username,
            password=req.password,
            port=req.port,
            git=str(req.git),
            pyfile=req.pyfile
        )
        return {"ok": True, "stdout": out, "stderr": err}

    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))
