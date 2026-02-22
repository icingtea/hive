"""Environment-based configuration for hive-hook.

Loads connection parameters from environment variables (or a ``.env``
file via ``python-dotenv``).  All three variables must be set:

- ``HIVE_POD_PORT`` — the port this agent's FastAPI server binds to.
- ``HIVE_MIND_ADDRESS`` — base URL of the hive mind (e.g.
  ``http://localhost:9000``).
- ``HIVE_MIND_COMMUNICATION_ENDPOINT`` — path on the hive mind that
  receives outbound messages (e.g. ``/pod``).

Usage::

    from hive_hook.config import HiveConfig

    config = HiveConfig.from_env()
    print(config.HIVE_POD_PORT)
"""

from __future__ import annotations

import os
import dotenv
from dataclasses import dataclass


@dataclass(frozen=True)
class HiveConfig:
    """Immutable configuration loaded from environment variables.

    Attributes:
        HIVE_POD_PORT: Port number for this agent's FastAPI server.
        HIVE_MIND_ADDRESS: Base URL of the hive mind server (no
            trailing slash).
        HIVE_MIND_COMMUICATION_ENDPOINT: URL path on the hive mind
            that accepts outbound messages from agents.
    """

    HIVE_POD_PORT: int
    HIVE_MIND_ADDRESS: str
    HIVE_MIND_COMMUICATION_ENDPOINT: str

    @staticmethod
    def from_env() -> HiveConfig:
        """Load configuration from environment variables.

        Calls ``dotenv.load_dotenv()`` first so that a ``.env`` file
        in the working directory is picked up automatically.

        Returns:
            A populated ``HiveConfig`` instance.

        Raises:
            RuntimeError: If any required environment variable is
                missing.
        """
        dotenv.load_dotenv()

        port_raw = os.getenv("HIVE_POD_PORT")
        if port_raw is None:
            raise RuntimeError("HIVE_POD_PORT is not set")

        mind_address = os.getenv("HIVE_MIND_ADDRESS")
        if mind_address is None:
            raise RuntimeError("HIVE_MIND_ADDRESS is not set")

        comm_endpoint = os.getenv("HIVE_MIND_COMMUNICATION_ENDPOINT")
        if comm_endpoint is None:
            raise RuntimeError("HIVE_MIND_COMMUNICATION_ENDPOINT is not set")

        return HiveConfig(
            HIVE_POD_PORT=int(port_raw),
            HIVE_MIND_ADDRESS=mind_address,
            HIVE_MIND_COMMUICATION_ENDPOINT=comm_endpoint,
        )
