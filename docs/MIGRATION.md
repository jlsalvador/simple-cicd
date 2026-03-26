# Migration Guide: v1alpha1 to v1alpha2

This guide explains how to migrate existing resources and covers the breaking
changes introduced in `v1alpha2` of the Simple CI/CD CRDs.

---

## Summary checklist

* [ ] Scale down the legacy simple-cicd-controller-manager.
* [ ] Wait for active WorkflowWebhookRequests to reach `DONE=true`.
* [ ] Delete all existing WorkflowWebhookRequest resources.
* [ ] Export and transform Workflow and WorkflowWebhook as `backup.yaml`.
* [ ] Clean up v1alpha1 legacy RBAC and Deployment resources.
* [ ] Apply the new v1alpha2 CRDs.
* [ ] Install the new operator deployment.
* [ ] Re-apply the updated resources from `backup.yaml`.

---

## Upgrade steps

> [!IMPORTANT]
> `WorkflowWebhookRequests` (WWR) are ephemeral operator-managed resources.
> Because `v1alpha1` stored request data inline while `v1alpha2` stores it in a
> separate `Secret`, existing WWRs cannot be automatically converted.
> **You must let them finish or delete them before upgrading**.

1. **Scale down the old controller.**
   In `v1alpha1`, the deployment was named `simple-cicd-controller-manager`.
   Scale it to 0 to prevent conflicts during migration.

   ```sh
   kubectl -n simple-cicd-system scale deployment simple-cicd-controller-manager --replicas=0
   ```

2. **Wait for running WWRs to finish.**
   Deleting a running WWR will interrupt its Jobs mid-execution.

   ```sh
   # Watch until no WWRs remain with DONE=false
   kubectl get wwr --all-namespaces --watch
   ```

   Once all WWRs show `DONE=true`, delete them:

   ```sh
   kubectl delete wwr --all --all-namespaces
   ```

3. **Export and convert existing Workflows.**
   Export your `Workflow` and `WorkflowWebhook` resources and update them.

   ```sh
   # 1. Export all resources to a backup file
   kubectl get workflow,workflowwebhook --all-namespaces -o yaml > backup.yaml

   # 2. Migrate (Update apiVersion and strip metadata)
   python3 -c '
      import sys,yaml
      f=sys.argv[1]
      d=yaml.safe_load(open(f))
      for i in d.get("items",[]):
         i["apiVersion"]="simple-cicd.jlsalvador.online/v1alpha2"
         i["metadata"]={k:v for k,v in i["metadata"].items() if k not in {"managedFields","resourceVersion","uid","creationTimestamp"}}
         i["metadata"].get("annotations",{}).pop("kubectl.kubernetes.io/last-applied-configuration",None)
      yaml.dump(d,open(f,"w"))
   ' backup.yaml
   ```

4. **Cleanup v1alpha1 legacy resources.**
   Remove the old installation to avoid "ghost" resources.

   ```sh
   kubectl delete -k 'github.com/jlsalvador/simple-cicd/config/default?ref=stable'
   ```

5. **Apply new CRDs and Operator.**
   Apply the `v1alpha2` CRDs and the new operator manifest.

   ```sh
   # Apply CRDs
   kubectl apply -f https://github.com/jlsalvador/simple-cicd/releases/latest/download/crds.yaml

   # Apply the new operator (Deployment 'operator', ServiceAccount 'operator')
   kubectl apply -f https://github.com/jlsalvador/simple-cicd/releases/latest/download/operator.yaml
   ```

6. **Restore your resources.**

   ```sh
   kubectl apply -f backup.yaml
   ```

---

## Overview of Changes

| Component | v1alpha1 (Legacy) | v1alpha2 (New) |
| --- | --- | --- |
| **Namespace** | `simple-cicd-system` | User-defined / `simple-cicd` |
| **Deployment** | `simple-cicd-controller-manager` | `operator` |
| **ServiceAccount** | `simple-cicd-controller-manager` | `operator` |
| **ClusterRole** | `simple-cicd-manager-role` | `operator` |
| **Legacy Roles** | Proxy, Metrics, Leader Election | *Removed* |

### Workflow & WorkflowWebhook

* **Breaking changes:** None.
* **New fields:** `Workflow` adds validation patterns. `WorkflowWebhook` adds
  `ttlSecondsAfterFinished` and `activeDeadlineSeconds`.

### WorkflowWebhookRequest (WWR)

* **Breaking change:**
  `spec.body`, `spec.headers`, `spec.host`, `spec.method`, and `spec.url` are
  **removed**.
* **New mechanism:**
  Request data is now stored in a dedicated `Secret` referenced by
  `spec.requestSecret`.
