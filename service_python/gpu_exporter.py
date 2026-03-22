import subprocess, time, re
from prometheus_client import start_http_server, Gauge

gpu_util = Gauge("nvidia_gpu_utilization", "GPU utilization %", ["gpu"])
gpu_mem = Gauge("nvidia_gpu_memory_used_mb", "GPU memory used MB", ["gpu"])
gpu_temp = Gauge("nvidia_gpu_temperature", "GPU temperature C", ["gpu"])


def collect():
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
