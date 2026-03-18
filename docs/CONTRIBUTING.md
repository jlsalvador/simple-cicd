# Contributing to Simple CI/CD

## Design rules

- Keep it Simple
- Be Explicit
- Embrace Minimalism

---

## Development

You'll need a Kubernetes cluster. [minikube] or [kind] work well locally.

```sh
# Start a local Kubernetes cluster with minikube.
minikube start

# Use minikube docker context.
eval $(minikube docker-env)

# Build an image for local development.
make docker-build IMAGE_REGISTRY=localhost VERSION=dev

# Deploy to the cluster through Helm.
make helm-install IMAGE_REGISTRY=localhost VERSION=dev
```

Run `make help` for the full list of available targets.

[minikube]: https://minikube.sigs.k8s.io
[kind]: https://sigs.k8s.io/kind
