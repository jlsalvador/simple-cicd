# Contributing to Simple CI/CD

## Design Rules

- Keep it simple
- Be explicit
- Embrace minimalism

---

## Development

You'll need a Kubernetes cluster. [minikube] or [kind] work well locally.

```sh
# Start a local Kubernetes cluster with minikube.
minikube start

# Install Custom Resource Definitions (CRDs).
make install-crds

# Start the operator (kubectl proxy will be launched).
make run
```

---

## Local deployment using Helm

```sh
# Start a local Kubernetes cluster with minikube.
minikube start

# Use the minikube Docker context.
eval $(minikube docker-env)

# Build an image for local development.
make docker-build IMAGE_REGISTRY=localhost VERSION=dev

# Deploy to the cluster through Helm.
make helm-install IMAGE_REGISTRY=localhost VERSION=dev
```

[minikube]: https://minikube.sigs.k8s.io
[kind]: https://kind.sigs.k8s.io
