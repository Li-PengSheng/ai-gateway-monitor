#!/bin/bash
# =============================================================
#  deploy.sh — build Docker images and deploy to K8s
#  Monitoring stack (Prometheus, Grafana, Jaeger) runs in
#  Docker Compose — NOT in K8s.
#
#  Usage:
#    ./deploy.sh            # full run: build + deploy + monitoring
#    ./deploy.sh build      # docker build images only
#    ./deploy.sh apply      # kubectl apply manifests only
#    ./deploy.sh monitor    # start docker compose monitoring stack
#    ./deploy.sh forward    # port-forward gateway (for prometheus scraping)
#    ./deploy.sh status     # show pod / service / hpa status
#    ./deploy.sh reset      # delete all K8s resources + stop compose
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

GO_IMAGE="go-gateway:v1"
PYTHON_IMAGE="python-ai:v1"

# K8s manifests — order matters
K8S_MANIFESTS=(
  "ollama-svc.yaml"           # ExternalName → WSL Ollama
  "python-ai.yaml"            # python-ai Deployment + Service
  "go-gateway.yaml"           # go-gateway Deployment + Service
  "go-gateway-hpa.yaml"       # HorizontalPodAutoscaler
  "go-gateway-monitor.yaml"   # ServiceMonitor (needs prometheus-operator)
)

# Docker Compose services for monitoring
COMPOSE_SERVICES="prometheus grafana jaeger"

# ── helpers ───────────────────────────────────────────────────
check_deps() {
  step "Checking dependencies"
  for cmd in docker kubectl; do
    command -v "$cmd" &>/dev/null \
      && success "$cmd found" \
      || error "$cmd is not installed or not in PATH"
  done

  if ! kubectl cluster-info &>/dev/null; then
    error "kubectl cannot reach a cluster — is minikube/k3s running?"
  fi
  success "kubectl cluster reachable"

  if ! docker compose version &>/dev/null; then
    warn "docker compose not available — monitoring stack won't start"
  else
    success "docker compose found"
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
  sed -i "s|- ip:.*|- ip: $WSL_IP|" "$SCRIPT_DIR/ollama-svc.yaml"
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
scrape_configs:
  - job_name: "go-ai-gateway"
    static_configs:
      - targets: ["$TARGET"]
EOF
    success "prometheus.yml created"
  fi
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

apply_manifests() {
  step "Applying K8s manifests"
  for f in "${K8S_MANIFESTS[@]}"; do
    path="$SCRIPT_DIR/$f"
    if [[ -f "$path" ]]; then
      info "Applying $f ..."
      if [[ "$f" == "go-gateway-monitor.yaml" ]]; then
        # ServiceMonitor needs prometheus-operator CRD — soft fail
        kubectl apply -f "$path" 2>/dev/null \
          && success "$f applied" \
          || warn "$f skipped — prometheus-operator (ServiceMonitor CRD) not installed"
      elif [[ "$f" == "go-gateway-hpa.yaml" ]]; then
        # HPA needs metrics-server — soft fail
        kubectl apply -f "$path" 2>/dev/null \
          && success "$f applied" \
          || warn "$f skipped — metrics-server may not be installed"
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
  info "  Prometheus → http://localhost:9090"
  info "  Grafana    → http://localhost:3000  (admin / admin)"
  info "  Jaeger     → http://localhost:16686"
  echo ""
  warn "Run './deploy.sh forward' in a new terminal so Prometheus can scrape the gateway"
}

port_forward_gateway() {
  step "Port-forwarding go-gateway-svc → localhost:8080"
  info "Prometheus will scrape metrics at http://localhost:8080/metrics"
  warn "Keep this terminal open — Ctrl+C to stop"
  echo ""
  kubectl port-forward svc/go-gateway-svc 8080:80
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

  if docker compose version &>/dev/null && [[ -f "$SCRIPT_DIR/docker-compose.yml" ]]; then
    echo -e "${BOLD}── Docker Compose ───────────────────────────────────${NC}"
    cd "$SCRIPT_DIR" && docker compose ps 2>/dev/null || true
    echo ""
  fi

  echo -e "${BOLD}── Quick test (run forward first) ───────────────────${NC}"
  echo "  # terminal A — expose gateway:"
  echo "  ./deploy.sh forward"
  echo ""
  echo "  # terminal B — test iris:"
  echo "  curl -X POST http://localhost:8080/predict/iris \\"
  echo "    -H 'Content-Type: application/json' \\"
  echo "    -d '{\"sepal_length\":6.0,\"sepal_width\":3.0,\"petal_length\":5.5,\"petal_width\":2.0}'"
  echo ""
  echo "  # terminal B — test model:"
  echo "  curl -X POST http://localhost:8080/predict/model \\"
  echo "    -H 'Content-Type: application/json' \\"
  echo "    -d '{\"prompt\":\"hello, who are you?\"}'"
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
    path="$SCRIPT_DIR/$f"
    if [[ -f "$path" ]]; then
      info "Deleting resources in $f ..."
      kubectl delete -f "$path" --ignore-not-found
    fi
  done
  success "K8s resources deleted"

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
    ;;
  apply)
    check_deps
    patch_ollama_svc
    patch_prometheus_target
    apply_manifests
    wait_for_pods
    show_status
    ;;
  monitor)
    patch_prometheus_target
    start_monitoring
    ;;
  forward)
    port_forward_gateway
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
  all|*)
    check_deps
    patch_ollama_svc
    patch_prometheus_target
    build_images
    apply_manifests
    wait_for_pods
    start_monitoring
    show_status
    ;;
esac