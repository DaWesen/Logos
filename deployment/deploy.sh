#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "========================================="
echo "  Logos Kubernetes Deployment (kind)"
echo "========================================="

INFRA_IMAGES=(
    "docker.1panel.live/bitnami/etcd:latest"
    "docker.1ms.run/library/postgres:latest"
    "docker.1ms.run/library/redis:latest"
    "docker.1panel.live/minio/minio:latest"
    "milvusdb/milvus:latest"
    "docker.m.daocloud.io/elasticsearch:8.19.5"
    "docker.1ms.run/neo4j:latest"
    "wurstmeister/kafka:latest"
    "wurstmeister/zookeeper:latest"
)

check_prerequisites() {
    echo "[1/8] Checking prerequisites..."
    for cmd in kind kubectl docker; do
        if ! command -v $cmd &> /dev/null; then
            echo "ERROR: $cmd is not installed. Please install it first."
            exit 1
        fi
    done
    echo "  All prerequisites met."
}

create_cluster() {
    echo "[2/8] Creating kind cluster..."
    if kind get clusters 2>/dev/null | grep -q "^logos$"; then
        echo "  Cluster 'logos' already exists, skipping."
    else
        kind create cluster --config "$SCRIPT_DIR/kind-cluster.yaml"
        echo "  Cluster created."
    fi
}

build_and_load_image() {
    echo "[3/8] Building Docker image..."
    docker build -t logos:latest "$PROJECT_DIR"
    echo "  Loading image into kind..."
    kind load docker-image logos:latest --name logos
    echo "  Image loaded."
}

load_infra_images() {
    echo "[4/8] Loading infrastructure images into kind..."
    for img in "${INFRA_IMAGES[@]}"; do
        echo "  Loading $img ..."
        kind load docker-image "$img" --name logos 2>/dev/null || echo "  WARNING: Failed to load $img, K8s will try to pull from registry."
    done
    echo "  Infrastructure images loaded."
}

deploy_namespace() {
    echo "[5/8] Creating namespace..."
    kubectl apply -f "$SCRIPT_DIR/namespace.yaml"
}

deploy_infra() {
    echo "[6/8] Deploying infrastructure..."
    kubectl apply -f "$SCRIPT_DIR/infra/etcd.yaml"
    kubectl apply -f "$SCRIPT_DIR/infra/postgres.yaml"
    kubectl apply -f "$SCRIPT_DIR/infra/redis.yaml"
    kubectl apply -f "$SCRIPT_DIR/infra/kafka.yaml"
    kubectl apply -f "$SCRIPT_DIR/infra/minio.yaml"
    kubectl apply -f "$SCRIPT_DIR/infra/milvus.yaml"
    kubectl apply -f "$SCRIPT_DIR/infra/elasticsearch.yaml"
    kubectl apply -f "$SCRIPT_DIR/infra/neo4j.yaml"
    echo "  Waiting for infrastructure to be ready..."
    kubectl wait deployment --all --for=condition=available -n logos --timeout=120s 2>/dev/null || true
}

deploy_config() {
    echo "[7/8] Deploying ConfigMap and Secrets..."
    kubectl apply -f "$SCRIPT_DIR/configmap.yaml"
}

deploy_services() {
    echo "[8/8] Deploying microservices..."
    kubectl apply -f "$SCRIPT_DIR/services/gateway.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/user.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/monitoring.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/billing.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/im.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/chat.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/contact.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/message.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/knowledge.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/search.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/vector.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/question.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/recommend.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/extraction.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/collection.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/bot.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/process.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/summary.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/mcp.yaml"
    kubectl apply -f "$SCRIPT_DIR/services/moderation.yaml"
    echo "  Waiting for services to be ready..."
    kubectl wait deployment --all --for=condition=available -n logos --timeout=180s 2>/dev/null || true
}

show_status() {
    echo ""
    echo "========================================="
    echo "  Deployment Complete!"
    echo "========================================="
    echo ""
    echo "  Gateway: http://localhost:30080"
    echo ""
    echo "  Useful commands:"
    echo "    kubectl get pods -n logos"
    echo "    kubectl logs -f deployment/gateway -n logos"
    echo "    kubectl get svc -n logos"
    echo ""
    kubectl get pods -n logos
}

check_prerequisites
create_cluster
build_and_load_image
load_infra_images
deploy_namespace
deploy_infra
deploy_config
deploy_services
show_status
