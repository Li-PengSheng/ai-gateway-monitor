#!/bin/bash
# =============================================================
#  deploy.sh — one-stop build / deploy / test helper
#
#  Two Prometheus instances (by design):
#    • Docker Compose :9090  — dev/Grafana/GPU, scrapes port-forwarded gateway
#    • In-cluster (monitoring ns) — per-pod scrape via ServiceMonitor → HPA
#
#  Common:
#    ./deploy.sh            # up: cluster + build + hpa-stack + deploy + monitoring
#    ./deploy.sh test       # end-to-end HPA proof: load test + watch scale-up
#    ./deploy.sh status     # pods / services / hpa / metrics API
#    ./deploy.sh reset      # delete K8s resources + helm stack + stop compose
#
#  Granular:
#    ./deploy.sh cluster    # create local kind cluster if none reachable
#    ./deploy.sh build      # docker build + load images into cluster
#    ./deploy.sh hpa-stack  # install kube-prometheus-stack + prometheus-adapter
#    ./deploy.sh apply      # hpa-stack + kubectl apply manifests
#    ./deploy.sh monitor    # start docker compose monitoring stack
#    ./deploy.sh forward    # port-forward gateway → localhost:8080 / :6060
#    ./deploy.sh loadtest [SECONDS]  # in-cluster load generator (default 120s)
#    ./deploy.sh verify-hpa # check custom.metrics.k8s.io + HPA metric values
#    ./deploy.sh gpu        # start GPU exporter (native)
#    ./deploy.sh logs       # tail logs from both deployments
# =============================================================

set -euo pipefail

# ── colours ──────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

info()    { echo -e "${BLUE}[INFO]${NC}  $*"; }
success() { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error()   { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }
step()    { echo -e "\n${CYAN}${BOLD}══ $* ${NC}"; }

# ── config ────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_SERVICE_DIR="$SCRIPT_DIR/service_go"
PYTHON_SERVICE_DIR="$SCRIPT_DIR/service_python"
K8S_DIR="$SCRIPT_DIR/k8s"

GO_IMAGE="go-gateway:v2"
PYTHON_IMAGE="python-ai:v2"

# K8s manifests — order matters (paths relative to k8s/)
K8S_MANIFESTS=(
  "ollama-svc.yaml"           # ExternalName → WSL Ollama
  "python-ai.yaml"            # python-ai Deployment + Service
  "go-gateway.yaml"           # go-gateway Deployment + Service
  "go-gateway-hpa.yaml"       # HorizontalPodAutoscaler
  "go-gateway-monitor.yaml"   # ServiceMonitor (needs prometheus-operator)
)

# Docker Compose services for monitoring
COMPOSE_SERVICES="prometheus grafana jaeger"

# In-cluster Prometheus stack (HPA custom metrics)
HPA_NAMESPACE="${HPA_NAMESPACE:-monitoring}"
PROMETHEUS_RELEASE="${PROMETHEUS_RELEASE:-prometheus}"
ADAPTER_RELEASE="${ADAPTER_RELEASE:-prometheus-adapter}"
PROMETHEUS_SVC="${PROMETHEUS_RELEASE}-kube-prometheus-prometheus.${HPA_NAMESPACE}.svc"

# Local kind cluster (auto-created when no cluster is reachable)
KIND_CLUSTER="${KIND_CLUSTER:-ai-gateway}"
# In-cluster load generator (HPA scale-up test)
LOADTEST_DURATION="${LOADTEST_DURATION:-120}"
LOADTEST_PARALLELISM="${LOADTEST_PARALLELISM:-40}"

# ── helpers ───────────────────────────────────────────────────
check_deps() {
  step "Checking dependencies"
  for cmd in docker kubectl; do
    command -v "$cmd" &>/dev/null \
      && success "$cmd found" \
      || error "$cmd is not installed or not in PATH"
  done

  ensure_cluster

  if ! docker compose version &>/dev/null; then
    warn "docker compose not available — monitoring stack won't start"
  else
    success "docker compose found"
  fi
}

# Ensure a reachable cluster; auto-create a local kind cluster when possible.
ensure_cluster() {
  if kubectl cluster-info &>/dev/null; then
    success "kubectl cluster reachable ($(kubectl config current-context 2>/dev/null))"
    return 0
  fi

  if command -v kind &>/dev/null; then
    warn "No cluster reachable — creating local kind cluster '$KIND_CLUSTER'"
    kind create cluster --name "$KIND_CLUSTER" --wait 3m
    kubectl config use-context "kind-$KIND_CLUSTER" >/dev/null 2>&1 || true
    success "kind cluster '$KIND_CLUSTER' ready"
  else
    error "kubectl cannot reach a cluster and 'kind' is not installed.
       Install kind (https://kind.sigs.k8s.io) or start minikube/k3s, then re-run."
  fi
}

patch_ollama_svc() {
  step "Patching ollama-svc.yaml with current WSL eth0 IP"
  WSL_IP=$(ip addr show eth0 | grep 'inet ' | awk '{print $2}' | cut -d/ -f1)
  if [[ -z "$WSL_IP" ]]; then
    warn "Could not detect WSL eth0 IP — skipping ollama-svc.yaml patch"
    return
  fi
  info "WSL eth0 IP: $WSL_IP"
  # Patch the ip field in Endpoints
  sed -i "s|- ip:.*|- ip: $WSL_IP|" "$K8S_DIR/ollama-svc.yaml"
  success "ollama-svc.yaml patched → $WSL_IP"
}

patch_prometheus_target() {
  step "Patching prometheus.yml scrape target"

  TARGET="host.docker.internal:8080"

  if [[ -f "$SCRIPT_DIR/prometheus.yml" ]]; then
    sed -i "s|- \".*:8080\"|- \"$TARGET\"|" "$SCRIPT_DIR/prometheus.yml"
    success "prometheus.yml target patched → $TARGET"
  else
    warn "prometheus.yml not found — creating default config"
    cat > "$SCRIPT_DIR/prometheus.yml" <<EOF
global:
  scrape_interval: 5s
  evaluation_interval: 5s
scrape_configs:
  - job_name: "go-ai-gateway"
    static_configs:
      - targets: ["$TARGET"]
EOF
    success "prometheus.yml created"
  fi
}

patch_prometheus_gpu() {
  step "Patching prometheus.yml GPU exporter target with WSL eth0 IP"

  WSL_IP=$(ip addr show eth0 | grep 'inet ' | awk '{print $2}' | cut -d/ -f1)
  if [[ -z "$WSL_IP" ]]; then
    warn "Could not detect WSL eth0 IP — skipping GPU target patch"
    return
  fi
  info "WSL eth0 IP: $WSL_IP"

  if [[ ! -f "$SCRIPT_DIR/prometheus.yml" ]]; then
    warn "prometheus.yml not found — skipping GPU target patch"
    return
  fi

  if grep -q 'job_name: "gpu"' "$SCRIPT_DIR/prometheus.yml"; then
    # Patch existing GPU target IP in place
    sed -i '/job_name: "gpu"/,/targets:/ s|- targets: \[\".*:9835\"\]|- targets: ["'"$WSL_IP"':9835"]|' "$SCRIPT_DIR/prometheus.yml"
    success "prometheus.yml GPU target patched → $WSL_IP:9835"
  else
    # Append if missing entirely
    cat >> "$SCRIPT_DIR/prometheus.yml" <<EOF

  - job_name: "gpu"
    static_configs:
      - targets: ["$WSL_IP:9835"]
EOF
    success "prometheus.yml GPU target added → $WSL_IP:9835"
  fi
}

patch_jaeger_endpoint() {
  step "Patching Jaeger endpoint with current WSL eth0 IP"
  WSL_IP=$(ip addr show eth0 | grep 'inet ' | awk '{print $2}' | cut -d/ -f1)
  if [[ -z "$WSL_IP" ]]; then
    warn "Could not detect WSL eth0 IP — skipping Jaeger patch"
    return
  fi
  info "WSL eth0 IP: $WSL_IP"

  # Patch go-gateway.yaml
  sed -i "s|value: \".*:4317\"|value: \"$WSL_IP:4317\"|" "$K8S_DIR/go-gateway.yaml"
  success "go-gateway.yaml Jaeger endpoint patched → $WSL_IP:4317"

  # Patch python-ai.yaml
  sed -i "s|value: \".*:4317\"|value: \"$WSL_IP:4317\"|" "$K8S_DIR/python-ai.yaml"
  success "python-ai.yaml Jaeger endpoint patched → $WSL_IP:4317"
}

build_images() {
  step "Building Docker images"

  if [[ -d "$GO_SERVICE_DIR" ]]; then
    info "Building $GO_IMAGE ..."
    docker build -t "$GO_IMAGE" "$GO_SERVICE_DIR"
    success "$GO_IMAGE built"
  else
    warn "service_go/ not found — skipping Go image"
  fi

  if [[ -d "$PYTHON_SERVICE_DIR" ]]; then
    info "Building $PYTHON_IMAGE ..."
    docker build -t "$PYTHON_IMAGE" "$PYTHON_SERVICE_DIR"
    success "$PYTHON_IMAGE built"
  else
    warn "service_python/ not found — skipping Python image"
  fi
}

load_images() {
  step "Loading Docker images into the cluster"

  # Use the active kubectl context as the source of truth
  CURRENT_CTX=$(kubectl config current-context 2>/dev/null || echo "")
  info "Active kubectl context: ${CURRENT_CTX:-<none>}"

  if [[ "$CURRENT_CTX" == *"minikube"* ]]; then
    info "minikube context — loading via 'minikube image load'"
    for img in "$GO_IMAGE" "$PYTHON_IMAGE"; do
      info "Loading $img ..."
      minikube image load "$img"
      success "$img loaded into minikube"
    done

  elif [[ "$CURRENT_CTX" == *"kind"* ]]; then
    # Extract cluster name from context (format: kind-<clustername>)
    KIND_CLUSTER="${CURRENT_CTX#kind-}"
    info "kind context — loading via 'kind load docker-image' (cluster: $KIND_CLUSTER)"
    for img in "$GO_IMAGE" "$PYTHON_IMAGE"; do
      info "Loading $img ..."
      kind load docker-image "$img" --name "$KIND_CLUSTER"
      success "$img loaded into kind"
    done

  elif [[ "$CURRENT_CTX" == *"k3s"* ]] || [[ "$CURRENT_CTX" == *"default"* && "$(command -v k3s)" ]]; then
    info "k3s context — importing via 'k3s ctr images import'"
    for img in "$GO_IMAGE" "$PYTHON_IMAGE"; do
      info "Loading $img ..."
      docker save "$img" | sudo k3s ctr images import -
      success "$img loaded into k3s"
    done

  else
    warn "Context '$CURRENT_CTX' is not a recognised local cluster (minikube/kind/k3s)."
    warn "Skipping image load — if using a remote registry, push manually:"
    warn "  docker tag $GO_IMAGE <registry>/$GO_IMAGE && docker push <registry>/$GO_IMAGE"
  fi
}

check_helm() {
  command -v helm &>/dev/null \
    && success "helm found" \
    || error "helm is required — https://helm.sh/docs/intro/install/"
}

hpa_stack_installed() {
  kubectl get crd servicemonitors.monitoring.coreos.com &>/dev/null
}

install_hpa_stack() {
  step "Installing in-cluster Prometheus stack (HPA metrics)"
  check_helm

  helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>/dev/null || true
  helm repo update prometheus-community

  info "Installing kube-prometheus-stack (release: $PROMETHEUS_RELEASE, ns: $HPA_NAMESPACE) ..."
  helm upgrade --install "$PROMETHEUS_RELEASE" prometheus-community/kube-prometheus-stack \
    -n "$HPA_NAMESPACE" --create-namespace \
    -f "$K8S_DIR/prometheus-stack-values.yaml" \
    --wait --timeout 10m
  success "kube-prometheus-stack ready"

  info "Installing prometheus-adapter (release: $ADAPTER_RELEASE) ..."
  helm upgrade --install "$ADAPTER_RELEASE" prometheus-community/prometheus-adapter \
    -n "$HPA_NAMESPACE" \
    -f "$K8S_DIR/adapter-values.yaml" \
    --wait --timeout 5m
  success "prometheus-adapter ready"

  verify_hpa_metrics_api
}

uninstall_hpa_stack() {
  step "Removing in-cluster Prometheus stack"
  if command -v helm &>/dev/null; then
    helm uninstall "$ADAPTER_RELEASE" -n "$HPA_NAMESPACE" 2>/dev/null \
      && success "$ADAPTER_RELEASE uninstalled" \
      || true
    helm uninstall "$PROMETHEUS_RELEASE" -n "$HPA_NAMESPACE" 2>/dev/null \
      && success "$PROMETHEUS_RELEASE uninstalled" \
      || true
  fi
  kubectl delete namespace "$HPA_NAMESPACE" --ignore-not-found \
    && success "namespace/$HPA_NAMESPACE deleted" \
    || true
}

verify_hpa_metrics_api() {
  step "Verifying custom.metrics.k8s.io API"
  for _ in $(seq 1 30); do
    if kubectl get apiservice v1beta1.custom.metrics.k8s.io &>/dev/null \
      && kubectl get --raw /apis/custom.metrics.k8s.io/v1beta1 2>/dev/null | grep -q '"kind":"APIResourceList"'; then
      success "custom.metrics.k8s.io API is available"
      return 0
    fi
    sleep 5
  done
  warn "custom.metrics.k8s.io not ready — run: ./deploy.sh verify-hpa"
  return 1
}

verify_hpa_pipeline() {
  step "Verifying HPA custom-metrics pipeline"

  if ! hpa_stack_installed; then
    warn "ServiceMonitor CRD missing — run: ./deploy.sh hpa-stack"
    return 1
  fi

  verify_hpa_metrics_api || true

  echo ""
  echo -e "${BOLD}── In-cluster Prometheus targets ────────────────────${NC}"
  if kubectl get pods -n "$HPA_NAMESPACE" -l "app.kubernetes.io/name=prometheus" &>/dev/null; then
    info "Port-forward cluster Prometheus (optional):"
    info "  kubectl port-forward -n $HPA_NAMESPACE svc/$PROMETHEUS_RELEASE-kube-prometheus-prometheus 9091:9090"
    info "  → http://localhost:9091  (docker Prometheus stays on :9090)"
  fi

  echo ""
  echo -e "${BOLD}── Per-pod p99_latency (custom.metrics.k8s.io) ────${NC}"
  NS=$(kubectl config view --minify -o jsonpath='{.contexts[0].context.namespace}' 2>/dev/null)
  NS="${NS:-default}"
  RAW=$(kubectl get --raw "/apis/custom.metrics.k8s.io/v1beta1/namespaces/${NS}/pods/*/p99_latency" 2>/dev/null || true)
  if [[ -n "$RAW" && "$RAW" != *"NotFound"* && "$RAW" != *"error"* ]]; then
    echo "$RAW" | python3 -m json.tool 2>/dev/null || echo "$RAW"
    success "p99_latency metric exposed to HPA"
  else
    warn "No p99_latency values yet — generate traffic (k6) and wait ~1m for rate()"
    info "  PromQL in cluster Prometheus:"
    info "  histogram_quantile(0.99, sum by (le, pod) (rate(http_request_duration_seconds_bucket[1m])))"
  fi

  echo ""
  echo -e "${BOLD}── HPA status ─────────────────────────────────────${NC}"
  kubectl describe hpa go-gateway-hpa 2>/dev/null \
    || warn "go-gateway-hpa not found — run: ./deploy.sh apply"
}

# In-cluster load generator — runs k6 (test/test.js) against go-gateway-svc so it
# reliably drives p99 latency up. No port-forward needed; then watches the HPA.
run_loadtest() {
  local duration="${1:-$LOADTEST_DURATION}"
  step "Load test — k6 traffic to go-gateway for ~${duration}s (VUs ${LOADTEST_PARALLELISM})"

  if ! kubectl get svc go-gateway-svc &>/dev/null; then
    error "go-gateway-svc not found — run: ./deploy.sh apply"
  fi

  kubectl delete job hpa-loadgen --ignore-not-found &>/dev/null || true

  info "Publishing k6 script (ConfigMap/k6-script) ..."
  kubectl create configmap k6-script \
    --from-file=test.js="$SCRIPT_DIR/test/test.js" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  cat <<EOF | kubectl apply -f - >/dev/null
apiVersion: batch/v1
kind: Job
metadata:
  name: hpa-loadgen
spec:
  ttlSecondsAfterFinished: 30
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: k6
          image: grafana/k6:latest
          args:
            - run
            - --vus=${LOADTEST_PARALLELISM}
            - --duration=${duration}s
            - --no-thresholds
            - --no-usage-report
            - --quiet
            - -e
            - BASE=http://go-gateway-svc
            - /scripts/test.js
          volumeMounts:
            - name: script
              mountPath: /scripts
      volumes:
        - name: script
          configMap:
            name: k6-script
EOF
  success "k6 load generator started (Job/hpa-loadgen)"

  info "Watching HPA — expect REPLICAS to climb above minReplicas ..."
  echo ""
  local start peak deadline
  start=$(date +%s)
  peak=0
  deadline=$(( start + duration + 45 ))
  printf "  %-8s  %-18s  %s\n" "ELAPSED" "TARGET(cur/500m)" "REPLICAS"
  while [ "$(date +%s)" -lt "$deadline" ]; do
    local line target reps
    line=$(kubectl get hpa go-gateway-hpa --no-headers 2>/dev/null || true)
    target=$(echo "$line" | awk '{print $3}')   # TARGETS column
    reps=$(echo "$line" | awk '{print $6}')     # REPLICAS column
    [[ "${reps:-x}" =~ ^[0-9]+$ ]] && (( reps > peak )) && peak=$reps
    printf "  %-8s  %-18s  %s\n" "$(( $(date +%s) - start ))s" "${target:-?}" "${reps:-?}"
    kubectl get job hpa-loadgen &>/dev/null || break
    sleep 15
  done

  echo ""
  if (( peak > 2 )); then
    success "HPA scaled up — peak replicas observed: ${peak}"
  else
    warn "Peak replicas: ${peak} — load may have been too light; retry with LOADTEST_PARALLELISM=80 ./deploy.sh loadtest"
  fi
  kubectl delete job hpa-loadgen --ignore-not-found &>/dev/null || true
  kubectl delete configmap k6-script --ignore-not-found &>/dev/null || true
  info "HPA will scale back down after its stabilization window (~5m)."
}

# End-to-end HPA proof: verify pipeline, generate load, confirm scale-up.
run_test() {
  step "End-to-end HPA custom-metrics test"

  if ! hpa_stack_installed; then
    error "HPA stack not installed — run: ./deploy.sh apply (or ./deploy.sh)"
  fi
  if ! kubectl get hpa go-gateway-hpa &>/dev/null; then
    error "go-gateway-hpa not found — run: ./deploy.sh apply"
  fi

  verify_hpa_metrics_api || true

  echo ""
  info "Baseline HPA state:"
  kubectl get hpa go-gateway-hpa 2>&1 || true

  run_loadtest "$LOADTEST_DURATION"

  echo ""
  echo -e "${BOLD}── Final HPA state + scaling events ───────────────${NC}"
  kubectl describe hpa go-gateway-hpa 2>/dev/null | sed -n '/Events:/,$p' || true
  echo ""
  kubectl get hpa go-gateway-hpa 2>&1 || true
  echo ""
  success "Test complete — see SuccessfulRescale events above for scale-up proof."
}

rollout_restart() {
  step "Forcing rollout restart to pick up new images"
  for deploy in go-gateway python-ai; do
    if kubectl get deployment "$deploy" &>/dev/null; then
      info "Restarting deployment/$deploy ..."
      kubectl rollout restart deployment/"$deploy"
      success "$deploy restarted"
    fi
  done
}

apply_manifests() {
  step "Applying K8s manifests"
  for f in "${K8S_MANIFESTS[@]}"; do
    path="$K8S_DIR/$f"
    if [[ -f "$path" ]]; then
      info "Applying $f ..."
      if [[ "$f" == "go-gateway-monitor.yaml" ]]; then
        if hpa_stack_installed; then
          kubectl apply -f "$path"
          success "$f applied"
        else
          warn "$f skipped — run './deploy.sh hpa-stack' first"
        fi
      elif [[ "$f" == "go-gateway-hpa.yaml" ]]; then
        kubectl apply -f "$path"
        success "$f applied"
        if ! kubectl get apiservice v1beta1.custom.metrics.k8s.io &>/dev/null; then
          warn "prometheus-adapter not ready — HPA may show <unknown> until ./deploy.sh hpa-stack completes"
        fi
      else
        kubectl apply -f "$path"
        success "$f applied"
      fi
    else
      warn "$f not found — skipping"
    fi
  done
}

wait_for_pods() {
  step "Waiting for pods to be ready (120s timeout)"
  for deploy in python-ai go-gateway; do
    if kubectl get deployment "$deploy" &>/dev/null; then
      info "Waiting for deployment/$deploy ..."
      kubectl rollout status deployment/"$deploy" --timeout=120s \
        && success "$deploy is ready" \
        || warn "$deploy not ready — run: kubectl logs -f deployment/$deploy"
    fi
  done
}

start_monitoring() {
  step "Starting monitoring stack (Docker Compose)"
  if ! docker compose version &>/dev/null; then
    warn "docker compose not found — skipping"
    return
  fi
  if [[ ! -f "$SCRIPT_DIR/docker-compose.yml" ]]; then
    warn "docker-compose.yml not found — skipping"
    return
  fi

  cd "$SCRIPT_DIR"
  info "Starting: $COMPOSE_SERVICES"
  docker compose up -d $COMPOSE_SERVICES
  success "Monitoring stack started"
  echo ""
  info "  Prometheus → http://localhost:9090  (docker — dev/Grafana)"
  info "  Grafana    → http://localhost:3000  (admin / admin)"
  info "  Jaeger     → http://localhost:16686"
  echo ""
  info "  In-cluster Prometheus → ./deploy.sh verify-hpa  (HPA metrics, optional :9091 port-forward)"
  echo ""
  warn "Run './deploy.sh forward' in a new terminal so Docker Prometheus can scrape the gateway"
}

start_gpu_exporter() {
  cd "$PYTHON_SERVICE_DIR"
  info "Launching gpu_exporter.py in background..."
  nohup uv run gpu_exporter.py > /tmp/gpu_exporter.log 2>&1 &
  GPU_PID=$!
  # wait for uv to spawn the real python process
  sleep 1
  PYTHON_PID=$(pgrep -f "gpu_exporter.py" | head -1)
  echo "${PYTHON_PID:-$GPU_PID}" > /tmp/gpu_exporter.pid

  success "GPU exporter started (PID: ${PYTHON_PID:-$GPU_PID})"
  info "  Metrics → http://localhost:9835/metrics"
  info "  Logs    → tail -f /tmp/gpu_exporter.log"
  warn "  Stop    → ./deploy.sh gpu-stop"
}

port_forward_gateway() {
  step "Port-forwarding go-gateway-svc → 0.0.0.0:8080"
  info "Prometheus will scrape metrics at http://host.docker.internal:8080/metrics"
  warn "Keep this terminal open — Ctrl+C to stop"
  echo ""
  kubectl port-forward --address 0.0.0.0 svc/go-gateway-svc 8080:80 &
  kubectl port-forward --address 0.0.0.0 svc/go-gateway-svc 6060:6060 &
  wait
}

show_status() {
  step "Cluster status"
  echo ""
  echo -e "${BOLD}── Pods ─────────────────────────────────────────────${NC}"
  kubectl get pods -o wide
  echo ""
  echo -e "${BOLD}── Services ─────────────────────────────────────────${NC}"
  kubectl get svc
  echo ""
  echo -e "${BOLD}── HPA ──────────────────────────────────────────────${NC}"
  kubectl get hpa 2>/dev/null || warn "No HPA resources found"
  echo ""

  if hpa_stack_installed; then
    echo -e "${BOLD}── HPA metrics stack (in-cluster) ─────────────────${NC}"
    kubectl get pods -n "$HPA_NAMESPACE" 2>/dev/null || true
    kubectl get apiservice v1beta1.custom.metrics.k8s.io 2>/dev/null || warn "prometheus-adapter APIService missing"
    echo ""
  fi

  if docker compose version &>/dev/null && [[ -f "$SCRIPT_DIR/docker-compose.yml" ]]; then
    echo -e "${BOLD}── Docker Compose ───────────────────────────────────${NC}"
    cd "$SCRIPT_DIR" && docker compose ps 2>/dev/null || true
    echo ""
  fi

  echo -e "${BOLD}── Next steps ───────────────────────────────────────${NC}"
  echo "  ./deploy.sh test       # end-to-end HPA proof (load + watch scale-up)"
  echo "  ./deploy.sh verify-hpa # inspect custom.metrics.k8s.io + HPA values"
  echo "  ./deploy.sh forward    # expose gateway → localhost:8080 (curl / k6)"
}

show_logs() {
  step "Tailing logs from all deployments (Ctrl+C to stop)"
  kubectl logs -f deployment/go-gateway --prefix=true &
  kubectl logs -f deployment/python-ai --prefix=true &
  wait
}

reset_all() {
  step "Deleting all K8s resources"
  for f in "${K8S_MANIFESTS[@]}"; do
    path="$K8S_DIR/$f"
    if [[ -f "$path" ]]; then
      info "Deleting resources in $f ..."
      kubectl delete -f "$path" --ignore-not-found
    fi
  done
  success "K8s resources deleted"

  uninstall_hpa_stack

  step "Stopping Docker Compose monitoring stack"
  if docker compose version &>/dev/null && [[ -f "$SCRIPT_DIR/docker-compose.yml" ]]; then
    cd "$SCRIPT_DIR"
    docker compose down
    success "Docker Compose stopped"
  fi
}

# ── entrypoint ────────────────────────────────────────────────
CMD="${1:-all}"

case "$CMD" in
  build)
    check_deps
    build_images
    load_images
    ;;
  apply)
    check_deps
    patch_ollama_svc
    patch_prometheus_target
    patch_prometheus_gpu
    patch_jaeger_endpoint
    install_hpa_stack
    apply_manifests
    rollout_restart
    wait_for_pods
    show_status
    ;;
  cluster)
    ensure_cluster
    ;;
  hpa-stack)
    check_deps
    install_hpa_stack
    ;;
  verify-hpa)
    check_deps
    verify_hpa_pipeline
    ;;
  loadtest)
    check_deps
    run_loadtest "${2:-$LOADTEST_DURATION}"
    ;;
  test)
    check_deps
    run_test
    ;;
  monitor)
    patch_prometheus_target
    patch_prometheus_gpu
    start_monitoring
    ;;
  forward)
    port_forward_gateway
    ;;
  gpu)
    patch_prometheus_gpu
    start_gpu_exporter
    info "Restarting Prometheus to reload config..."
    cd "$SCRIPT_DIR" && docker compose restart prometheus 2>/dev/null \
      && success "Prometheus restarted" \
      || warn "Prometheus not running — start it with: ./deploy.sh monitor"
    ;;
  status)
    show_status
    ;;
  logs)
    show_logs
    ;;
  reset)
    reset_all
    ;;
  gpu-stop)
    if [[ -f /tmp/gpu_exporter.pid ]]; then
      PID=$(cat /tmp/gpu_exporter.pid)
      if kill -0 "$PID" 2>/dev/null; then
        kill "$PID"
        rm /tmp/gpu_exporter.pid
        success "GPU exporter stopped (PID: $PID)"
      else
        warn "Process $PID is not running (already dead or never started)"
        rm /tmp/gpu_exporter.pid
      fi
    else
      # fallback: try pkill by script name
      if pgrep -f "gpu_exporter.py" &>/dev/null; then
        pkill -f "gpu_exporter.py"
        success "GPU exporter stopped (via pkill)"
      else
        warn "No GPU exporter process found"
      fi
    fi
    ;;
  up|all|*)
    check_deps
    patch_ollama_svc
    patch_prometheus_target
    patch_prometheus_gpu
    patch_jaeger_endpoint
    build_images
    load_images
    install_hpa_stack
    apply_manifests
    rollout_restart
    wait_for_pods
    start_monitoring
    show_status
    ;;
esac