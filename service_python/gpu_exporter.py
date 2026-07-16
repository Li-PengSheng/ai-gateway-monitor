"""GPU metrics exporter for Prometheus.

Polls nvidia-smi every 5 seconds and exposes GPU utilization, memory usage,
and temperature as Prometheus gauges on port 9835.

Run independently of Docker Compose::

    python gpu_exporter.py
"""

import subprocess
import time

from prometheus_client import Gauge, start_http_server

gpu_util = Gauge("nvidia_gpu_utilization", "GPU utilization %", ["gpu"])
gpu_mem = Gauge("nvidia_gpu_memory_used_mb", "GPU memory used MB", ["gpu"])
gpu_temp = Gauge("nvidia_gpu_temperature", "GPU temperature C", ["gpu"])


def collect() -> None:
    """Query nvidia-smi and update ``gpu_util`` / ``gpu_mem`` / ``gpu_temp``.

    The main loop does not catch errors, so a failed poll crashes the process
    (fail-loud so missing scrapes surface the outage).

    Returns:
        None.

    Raises:
        FileNotFoundError: If the nvidia-smi executable is not installed.
        subprocess.CalledProcessError: If nvidia-smi exits non-zero.
        ValueError: If a CSV line does not split into exactly four fields.
    """
    out = subprocess.check_output(
        [
            "nvidia-smi",
            "--query-gpu=index,utilization.gpu,memory.used,temperature.gpu",
            "--format=csv,noheader,nounits",
        ]
    ).decode()
    for line in out.strip().split("\n"):
        idx, util, mem, temp = [x.strip() for x in line.split(",")]
        gpu_util.labels(gpu=idx).set(float(util))
        gpu_mem.labels(gpu=idx).set(float(mem))
        gpu_temp.labels(gpu=idx).set(float(temp))


if __name__ == "__main__":
    start_http_server(9835)
    print("GPU exporter running on :9835")
    while True:
        collect()
        time.sleep(5)
