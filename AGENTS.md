# fleet-manager - AI Agent Guide

## Project Structure

This is a **raw controller-runtime project** — it is not managed by the kubebuilder CLI.
There is no `PROJECT` file, no `controller-gen`, and no CRDs. Everything under `config/`
is hand-written and hand-maintained.

```
cmd/main.go                    Manager entry (registers the Deployment controller)
internal/controller/*          Reconciliation logic (watches built-in apps/v1 Deployments)
config/*.yaml                  Flat, hand-maintained Kubernetes manifests (Namespace, RBAC, Deployment)
config/kustomization.yaml      Namespace/name-prefix + resource list for `make deploy`/`make build-installer`
htcondor/*                     supervisord config baked into the container image (condor_master + manager)
Makefile                       Build/test/deploy commands
test/e2e/*                     Kind-based end-to-end suite (build tag: e2e)
```

There are no CRD schemas, no `zz_generated.*.go`, and no webhook scaffolding in this project.
If a future need arises for a CRD or webhook, either hand-write it against controller-runtime
directly, or reintroduce kubebuilder deliberately (`kubebuilder init` into a fresh layout) rather
than assuming any of the conventions below still apply.

## Critical Rules

### config/*.yaml is hand-maintained — edit it directly
`config/role.yaml`, `config/role_binding.yaml`, `config/service_account.yaml`, and
`config/manager.yaml` are plain YAML, not generated output. When a controller needs a new
permission, add the rule to `config/role.yaml` yourself — there is no `make manifests` step
that will do it for you.

### No RBAC-marker-driven generation
`internal/controller/deployment_controller.go` may still carry `// +kubebuilder:rbac:...`
marker comments from when this project *was* kubebuilder-scaffolded. They are inert — nothing
consumes them anymore. Don't add new behavior on the assumption they drive codegen; the source
of truth for RBAC is `config/role.yaml`.

### E2E Tests Require an Isolated Kind Cluster
The e2e tests are designed to validate the solution in an isolated environment (similar to GitHub Actions CI).
Ensure you run them against a dedicated [Kind](https://kind.sigs.k8s.io/) cluster (not your "real" dev/prod cluster).

## After Making Changes

**After editing `*.go` files:**
```bash
make lint-fix   # Auto-fix code style
make test       # Run unit tests (envtest: real K8s API + etcd, no CRDs to install)
```

**After editing `config/*.yaml`:**
Just edit the file. Validate with `kustomize build config` before deploying.

## Testing & Development

```bash
make test              # Run unit tests (uses envtest: real K8s API + etcd)
make run               # Run locally (uses current kubeconfig context)
```

Tests use **Ginkgo + Gomega** (BDD style). Check `internal/controller/suite_test.go` for setup.

## Deployment Workflow

```bash
export IMG=<registry>/fleet-manager:tag
make docker-build docker-push IMG=$IMG   # Or: kind load docker-image $IMG --name <cluster>
make deploy IMG=$IMG                     # kustomize build config | kubectl apply -f -
make undeploy                            # tear down
```

### Controller Design

**Implementation rules:**
- **Idempotent reconciliation**: Safe to run multiple times
- **Re-fetch before updates**: `r.Get(ctx, req.NamespacedName, obj)` before `r.Update` to avoid conflicts
- **Structured logging**: `log := log.FromContext(ctx); log.Info("msg", "key", val)`
- **Watch secondary resources**: Use `.Owns()` or `.Watches()`, not just `RequeueAfter`

### Logging

**Follow Kubernetes logging message style guidelines:**

- Start from a capital letter
- Do not end the message with a period
- Active voice: subject present (`"Deployment could not create Pod"`) or omitted (`"Could not create Pod"`)
- Past tense: `"Could not delete Pod"` not `"Cannot delete Pod"`
- Specify object type: `"Deleted Pod"` not `"Deleted"`
- Balanced key-value pairs

```go
log.Info("Starting reconciliation")
log.Info("Created Deployment", "name", deploy.Name)
log.Error(err, "Failed to create Pod", "name", name)
```

**Reference:** https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md#message-style-guidelines

## Distribution

### YAML Bundle (Kustomize)

```bash
# Generate dist/install.yaml from Kustomize manifests
make build-installer IMG=<registry>/fleet-manager:tag
```

- `dist/install.yaml` is generated from the manifests under `config/` (Namespace, RBAC, Deployment)
- Commit this file to your repository for easy distribution
- Users only need `kubectl` to install (no additional tools required)

**Example:** Users install with a single command:
```bash
kubectl apply -f https://raw.githubusercontent.com/<org>/fleet-manager/<tag>/dist/install.yaml
```

### Publish Container Image

```bash
export IMG=<registry>/fleet-manager:<version>
make docker-build docker-push IMG=$IMG
```

## References

### Essential Reading
- **controller-runtime FAQ**: https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md (common patterns and questions)
- **Good Practices**: https://book.kubebuilder.io/reference/good-practices.html (why reconciliation is idempotent, status conditions, etc. — still a good reference even outside kubebuilder)
- **Logging Conventions**: https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md#message-style-guidelines (message style, verbosity levels)

### API Design & Implementation
- **API Conventions**: https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md
- **Operator Pattern**: https://kubernetes.io/docs/concepts/extend-kubernetes/operator/

### Tools & Libraries
- **controller-runtime**: https://github.com/kubernetes-sigs/controller-runtime
