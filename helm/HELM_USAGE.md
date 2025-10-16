# Forkspacer Helm Chart Usage Guide

This guide explains how to deploy Forkspacer using Helm in different environments.

## Prerequisites

- Kubernetes cluster (v1.20+)
- Helm 3.x
- kubectl configured to access your cluster

## Quick Start

### 1. Basic Installation (Operator Only)
```bash
helm install forkspacer ./helm \
  --namespace forkspacer-system \
  --create-namespace
```

### 2. With UI Enabled (Development)
```bash
helm install forkspacer ./helm \
  --set operator-ui.enabled=true \
  --namespace forkspacer-system \
  --create-namespace
```

### 3. Production Setup with Ingress
```bash
helm install forkspacer ./helm \
  --set operator-ui.enabled=true \
  --set ingress.enabled=true \
  --namespace forkspacer-system \
  --create-namespace
```

## Deployment Modes

### Mode 1: Port-Forward (Development)
**Best for:** Local development, testing
**Services:** ClusterIP
**Access:** kubectl port-forward

```bash
helm install forkspacer ./helm \
  --set operator-ui.enabled=true \
  --namespace forkspacer-system \
  --create-namespace

# Access frontend
kubectl port-forward svc/operator-ui 3000:80 -n forkspacer-system

# Access API
kubectl port-forward svc/forkspacer-api-server 8421:8080 -n forkspacer-system
```

### Mode 2: NodePort (Local Clusters)
**Best for:** Local clusters (kind, minikube), development
**Services:** NodePort
**Access:** Direct node IP + port

```bash
helm install forkspacer ./helm \
  --set operator-ui.enabled=true \
  --set operator-ui.service.type=NodePort \
  --set api-server.service.type=NodePort \
  --namespace forkspacer-system \
  --create-namespace

# Get access URLs
NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
UI_PORT=$(kubectl get svc operator-ui -n forkspacer-system -o jsonpath='{.spec.ports[0].nodePort}')
echo "UI: http://$NODE_IP:$UI_PORT"
```

### Mode 3: Ingress (Production)
**Best for:** Production environments
**Services:** ClusterIP
**Access:** Domain name through ingress
**Requires:** Ingress controller (nginx, traefik, etc.) installed in your cluster

```bash
# Install Forkspacer with unified ingress
helm install forkspacer ./helm \
  --set operator-ui.enabled=true \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=forkspacer.yourdomain.com \
  --namespace forkspacer-system \
  --create-namespace
```

**How it works:**
- Single ingress routes both UI and API
- `/api/*` → API Server (port 8080)
- `/*` → UI (port 80)
- UI uses relative paths - no CORS issues

## Configuration Options

### Core Components
```yaml
# Operator (always enabled)
controllerManager:
  replicas: 1
  
# API Server (required when UI is enabled)
api-server:
  enabled: true  # Cannot be disabled when operator-ui.enabled=true

# Frontend UI (optional)
operator-ui:
  enabled: false  # Set to true to enable web interface
```

### Service Types
```yaml
# ClusterIP (default)
operator-ui:
  service:
    type: ClusterIP
    
# NodePort (for direct access)
operator-ui:
  service:
    type: NodePort
    # nodePort: 30080  # Optional: specify port
```

### Ingress Configuration
```yaml
# Unified ingress for both UI and API
ingress:
  enabled: true
  className: nginx  # or your ingress controller class
  hosts:
    - host: forkspacer.yourdomain.com
  annotations: {}  # Add your ingress annotations here
  tls: []  # Configure TLS if needed
```

## Important: Frontend API Configuration

The frontend UI is built to use relative paths (`/api/v1`) by default. This works perfectly with the unified ingress setup - no custom builds needed!

## Environment Examples

### Local Development (kind)
```bash
# Create kind cluster
kind create cluster --name forkspacer

# Install nginx ingress controller for Kind
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml
kubectl wait --namespace ingress-nginx --for=condition=ready pod --selector=app.kubernetes.io/component=controller --timeout=90s

# Install with ingress
helm install forkspacer ./helm \
  --set operator-ui.enabled=true \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=forkspacer.local

# Add to /etc/hosts
echo "$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}') forkspacer.local" | sudo tee -a /etc/hosts

# Get ingress NodePort
INGRESS_PORT=$(kubectl get svc -n ingress-nginx ingress-nginx-controller -o jsonpath='{.spec.ports[?(@.name=="http")].nodePort}')
echo "Access at: http://forkspacer.local:$INGRESS_PORT"
```

### Production Deployment

#### Option 1: Using values file (Recommended)
```bash
# Create values-production.yaml
cat <<EOF > values-production.yaml
operator-ui:
  enabled: true

ingress:
  enabled: true
  className: nginx
  hosts:
    - host: app.yourcompany.com
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
  tls:
    - secretName: forkspacer-tls
      hosts:
        - app.yourcompany.com
EOF

# Install with values file
helm install forkspacer ./helm -f values-production.yaml
```

#### Option 2: Using command line (without TLS)
```bash
# Simple HTTP-only installation
helm install forkspacer ./helm \
  --set operator-ui.enabled=true \
  --set ingress.enabled=true \
  --set 'ingress.hosts[0].host=app.yourcompany.com' \
  --set ingress.className=nginx
```

## Troubleshooting

### CORS Issues
If you see CORS errors in the browser:
1. Use ingress mode (same origin for frontend/backend)
2. Or rebuild frontend with correct API URL
3. Check that API server is accessible from frontend

### DNS Resolution
For local testing with ingress, add your domain to `/etc/hosts`:
```bash
# For local clusters (Kind, Minikube)
echo "$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}') forkspacer.local" | sudo tee -a /etc/hosts

# Then access via ingress controller NodePort (if using NodePort type)
kubectl get svc -n ingress-nginx
```

### Service Discovery
Remember:
- ClusterIP services only work **inside** the cluster
- Service names (e.g., `forkspacer-api-server`) only resolve **inside** the cluster
- For external access, use NodePort, LoadBalancer, or Ingress

## Upgrading

```bash
# Upgrade with new values
helm upgrade forkspacer ./helm --set operator-ui.enabled=true -n forkspacer-system

# Check upgrade status
helm status forkspacer -n forkspacer-system
```

## Uninstalling

```bash
helm uninstall forkspacer -n forkspacer-system
```