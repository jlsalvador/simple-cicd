# Simple CI/CD

![Simple CI/CD Logo](docs/img/logo.png)

**Manage jobs, workflows, and webhooks natively via K8s APIs.**

No dependencies, no virtual machines, no Docker-in-Docker, no need to learn a
new language or framework.

---

## Getting Started

### Install via Helm (recommended)

```sh
helm upgrade --install \
  --create-namespace \
  --namespace simple-cicd \
  --repo https://jlsalvador.github.io/simple-cicd \
  operator operator
```

### Install via manifest

```sh
kubectl apply -f https://github.com/jlsalvador/simple-cicd/releases/latest/download/operator.yaml
```

---

## How It Works

Simple CI/CD follows the Kubernetes [Operator pattern].

The operator exposes an HTTP server on port `9000`. Each incoming request to
`/{namespace}/{workflowWebhookName}` creates a `WorkflowWebhookRequest` and
starts the reconciliation loop.

```mermaid
flowchart TD
    A["HTTP POST /{namespace}/{webhookName}"]
    A --> C[Request Secret]
    A --> B[WorkflowWebhookRequest]

    B -->|reads| D[WorkflowWebhook]
    D --> E[Workflow]
    E -->|clone| F[Jobs]
    C -->|mounted into every Job pod| F

    F --> G{watch Job status}

    G -->|success| H["next Workflow(s)<br>when: OnSuccess / Always"]
    G -->|failure| I["next Workflow(s)<br>when: OnFailure / Always"]

    H --> J{more Workflows?}
    I --> J
    J -->|no| K([WorkflowWebhookRequest done])
    J -->|yes| E
```

### Reconciliation loop

1. A webhook HTTP request arrives. The operator creates a `Secret` containing
   the request data and a `WorkflowWebhookRequest` (WWR) referencing it.
2. The reconciler reads the WWR, resolves the referenced `WorkflowWebhook` and
   its `Workflow` list.
3. For each active Workflow, referenced Jobs are cloned. Jobs in the same
   namespace as the WWR mount the request Secret directly. Jobs in other
   namespaces receive a per-job mirrored copy of the Secret, owned by the Job
   and automatically garbage-collected when the Job is deleted.
4. The reconciler polls cloned Jobs until all finish, then evaluates `when`
   conditions to determine which next Workflows to trigger.
5. Steps 3-4 repeat until no further Workflows remain, at which point the WWR
   is marked `done: true`.

### Sequence diagram

```mermaid
sequenceDiagram
  participant Client
  participant Operator as Simple CI/CD Operator
  participant Step1 as Step 1 Pod(s)
  participant Step2 as Step 2 Pod(s)

  Client->>Operator: HTTP POST /{namespace}/{webhookName}
  Operator->>Operator: Create request Secret + WorkflowWebhookRequest
  Operator->>Step1: Clone and run Workflow step 1 Job(s)
  Step1-->>Operator: Exit status

  alt next Workflow condition met
    Operator->>Step2: Clone and run Workflow step 2 Job(s)
    Step2-->>Operator: Exit status
  end

  Operator->>Operator: Mark WorkflowWebhookRequest as done
```

---

## Custom Resources

### Workflow

Defines which Jobs to clone and which Workflows to trigger next based on exit
status.

```yaml
apiVersion: simple-cicd.jlsalvador.online/v1alpha2
kind: Workflow
metadata:
  name: my-workflow
  namespace: example
spec:
  jobsToBeCloned:
    - name: my-job
      namespace: example # Defaults to the Workflow's own namespace when omitted.
  next:
    - name: my-next-workflow
      when: OnAnyFailure # OnSuccess | OnAnySuccess | OnFailure | OnAnyFailure | Always
  suspend: false # Set to true to skip execution without deleting.
```

### WorkflowWebhook

Binds an HTTP path to one or more Workflows and controls concurrency and
lifecycle policies. All policy fields are copied into each
`WorkflowWebhookRequest` at creation time and are not affected by later changes
to the `WorkflowWebhook`.

```yaml
apiVersion: simple-cicd.jlsalvador.online/v1alpha2
kind: WorkflowWebhook
metadata:
  name: my-webhook
  namespace: example
spec:
  workflows:
    - name: my-workflow
      namespace: example   # Defaults to the WorkflowWebhook's own namespace when omitted.
  concurrencyPolicy: Allow # Allow | Forbid | Replace
  suspend: false
  ttlSecondsAfterFinished: 3600 # Delete the WWR 1 hour after it finishes. Omit to keep it forever.
  activeDeadlineSeconds: 1800   # Mark the WWR done after 30 min if still running. Omit to disable.
```

#### Concurrency policies

| Policy    | Behaviour                                       |
| --------- | ----------------------------------------------- |
| `Allow`   | Multiple WWRs can run simultaneously (default). |
| `Forbid`  | Creates the WWR without executing any Workflow. |
| `Replace` | Deletes any running WWR and starts a fresh one. |

> **ℹ️ Note:**
> With `Forbid`, the HTTP response is still `202 Accepted`. The WWR
> is created and immediately marked done. Inspect `status.conditions` to
> determine whether execution was actually skipped.

#### `when` conditions

| Value          | Trigger condition                        |
| -------------- | ---------------------------------------- |
| `OnSuccess`    | All Jobs in the step succeeded (default) |
| `OnAnySuccess` | At least one Job succeeded               |
| `OnFailure`    | All Jobs in the step failed              |
| `OnAnyFailure` | At least one Job failed                  |
| `Always`       | Always trigger regardless of outcome     |

#### `ttlSecondsAfterFinished`

When set, the operator automatically deletes the WWR the specified number of
seconds after it completes. `0` means delete immediately on completion. Omitting
the field keeps the WWR indefinitely.

#### `activeDeadlineSeconds`

When set, the operator forcibly terminates all running Jobs and marks the WWR
done with reason `DeadlineExceeded` if it has not completed within the specified
number of seconds of its creation. Omitting the field disables the deadline.

> **ℹ️ Note:**
> `activeDeadlineSeconds` is measured from `metadata.creationTimestamp`, not
> from when execution actually started, making it a hard wall-clock limit on
> the total time a request may occupy the system.

### WorkflowWebhookRequest

Created automatically by the operator on each incoming HTTP request. Tracks the
full execution lifecycle.

```sh
kubectl get wwr -n example

NAME                            DONE   WEBHOOK                   SUCCESSFUL JOBS   FAILED JOBS   AGE
workflowwebhook-example-b5lmh   true   workflowwebhook-example   1                 0             76s
workflowwebhook-example-lw2ws   true   workflowwebhook-example   1                 1             12m
```

#### Status conditions

The `status.conditions` field records lifecycle events. The most recent
condition reflects the final outcome:

| `reason`           | Meaning                           |
| ------------------ | --------------------------------- |
| `Done`             | All Workflows completed normally. |
| `Suspended`        | No Workflows were executed.       |
| `Forbidden`        | Another WWR was running.          |
| `NoWorkflows`      | Nothing to execute.               |
| `DeadlineExceeded` | Running Jobs were terminated.     |

```sh
kubectl get wwr -n example -o jsonpath='{.items[-1].status.conditions[-1]}'
```

---

## Request Data in Jobs

Every Job cloned by the operator has the original HTTP request data mounted as
read-only files at `/var/run/secrets/kubernetes.io/request/` inside all its
containers. The data is stored in a Kubernetes `Secret` (named
`{webhookName}-request-{random}` in the WWR's namespace) and either mounted
directly for same-namespace Jobs or mirrored into the Job's own namespace for
cross-namespace Jobs.

| File         | Content                                        |
| ------------ | ---------------------------------------------- |
| `body`       | Request body                                   |
| `headers`    | All headers serialised as JSON                 |
| `host`       | Host header value                              |
| `method`     | HTTP method (`GET`, `POST`, …)                 |
| `url`        | Full request URL                               |
| `remoteAddr` | Client IP and port (e.g. `10.0.0.5:54321`)     |
| `timestamp`  | Time the request was received (UNIX timestamp) |

> **⚠️ Important:**
> Job templates **must** have `spec.suspend: true`. Without it,
> Kubernetes will run the Job immediately when it is created as a template,
> before the operator has a chance to clone it. The operator sets
> `spec.suspend: false` on each cloned copy automatically.

---

## Example

This example creates a workflow that runs a job, echoing the original HTTP
request and returning a random exit code. On failure, it triggers a second
workflow that echoes "ERROR".

```yaml
# Job that randomly exits with code 0 or 1.
apiVersion: batch/v1
kind: Job
metadata:
  name: job-example-random-exit
  namespace: example
spec:
  suspend: true # Prevents Kubernetes from running this directly.
  backoffLimit: 0
  template:
    spec:
      containers:
        - name: random-exit
          image: bash
          command: ["sh", "-c", "exit $$(($RANDOM % 2))"]
        - name: echo-request
          image: bash
          command:
            - sh
            - -c
            - |
              echo "Host:       $(cat /var/run/secrets/kubernetes.io/request/host)"
              echo "Headers:    $(cat /var/run/secrets/kubernetes.io/request/headers)"
              echo "Method:     $(cat /var/run/secrets/kubernetes.io/request/method)"
              echo "URL:        $(cat /var/run/secrets/kubernetes.io/request/url)"
              echo "RemoteAddr: $(cat /var/run/secrets/kubernetes.io/request/remoteAddr)"
              echo "Timestamp:  $(cat /var/run/secrets/kubernetes.io/request/timestamp)"
              echo "Body:       $(cat /var/run/secrets/kubernetes.io/request/body)"
      restartPolicy: Never # Do not re-run the pod if something fails.
---
# Job that echoes "ERROR".
apiVersion: batch/v1
kind: Job
metadata:
  name: job-example-error
  namespace: example
spec:
  suspend: true
  template:
    spec:
      containers:
        - name: error
          image: bash
          command: ["echo", "ERROR"]
      restartPolicy: Never
---
# Workflow triggered on failure: runs job-example-error.
apiVersion: simple-cicd.jlsalvador.online/v1alpha2
kind: Workflow
metadata:
  name: workflow-example-on-failure
  namespace: example
spec:
  jobsToBeCloned:
    - name: job-example-error
---
# Main workflow: runs job-example-random-exit, then workflow-example-on-failure on any failure.
apiVersion: simple-cicd.jlsalvador.online/v1alpha2
kind: Workflow
metadata:
  name: workflow-example
  namespace: example
spec:
  jobsToBeCloned:
    - name: job-example-random-exit
  next:
    - name: workflow-example-on-failure
      when: OnAnyFailure
---
# WorkflowWebhook: listens on /example/workflowwebhook-example.
apiVersion: simple-cicd.jlsalvador.online/v1alpha2
kind: WorkflowWebhook
metadata:
  name: workflowwebhook-example
  namespace: example
spec:
  workflows:
    - name: workflow-example
  ttlSecondsAfterFinished: 3600 # Clean up WWRs after 1 hour.
```

Trigger the WorkflowWebhook via `kubectl port-forward`:

```sh
kubectl -n simple-cicd port-forward svc/operator 9000:9000 &
curl -XPOST http://localhost:9000/example/workflowwebhook-example
```

> **ℹ️ Note:**
> You can also reach the operator directly from within the cluster or expose it
> through any Service. Remember to set up proper authentication & authorization.

---

## Motivation

Existing solutions either impose excessive requirements or require virtual
machines, Docker-in-Docker, or components outside the Kubernetes ecosystem.

Simple CI/CD uses native Kubernetes Jobs, so you do not need to learn a new
language.

<!-- markdownlint-disable MD033 -->
<table>
  <caption>Disclaimer: based on personal opinion. Please do your own research.</caption>
  <thead>
    <tr>
      <th>Alternative</th>
      <th>Advantages</th>
      <th>Disadvantages</th>
    </tr>
  </thead>
  <tbody>
    <tr>
      <td><a href="https://github.com/features/actions">GitHub Actions</a></td>
      <td><ul>
        <li>De-facto standard for public repositories on GitHub.</li>
      </ul></td>
      <td><ul>
        <li>Controlled by Microsoft.</li>
        <li>Closed garden.</li>
        <li>Actions must be published publicly.</li>
        <li>Limited by a paywall.</li>
        <li>Not suitable for air-gap environments or private clusters.</li>
      </ul></td>
    </tr>
    <tr>
      <td><a href="https://www.jenkins.io">Jenkins</a></td>
      <td><ul>
        <li>Open-source.</li>
        <li>Mature and battle-tested.</li>
        <li>Backed by a broad community.</li>
      </ul></td>
      <td><ul>
        <li>Resource hungry.</li>
        <li>Requires Groovy for advanced pipelines.</li>
        <li>Needs add-ons for Kubernetes integration.</li>
        <li>Limited CLI support.</li>
      </ul></td>
    </tr>
    <tr>
      <td><a href="https://tekton.dev">Tekton</a></td>
      <td><ul>
        <li>Open-source.</li>
        <li>Backed by the <a href="https://cd.foundation">CD Foundation</a>.</li>
      </ul></td>
      <td><ul>
        <li>Cannot use more than one PersistentVolume per Pod.</li>
        <li>Frequent API churn leading to deprecated Catalog tasks.</li>
      </ul></td>
    </tr>
    <tr>
      <td><a href="https://drone.io">Drone</a></td>
      <td><ul>
        <li>Open-source.</li>
      </ul></td>
      <td><ul>
        <li>Controlled by <a href="https://www.harness.io">Harness</a>.</li>
        <li>Community pull requests receive limited attention.</li>
      </ul></td>
    </tr>
    <tr>
      <td><a href="https://woodpecker-ci.org">Woodpecker CI</a></td>
      <td><ul>
        <li>Open-source community fork of Drone.</li>
        <li>Community supported.</li>
      </ul></td>
      <td><ul>
        <li>No namespaced PersistentVolume support across tasks.</li>
        <li>Limited Kubernetes support.</li>
      </ul></td>
    </tr>
    <tr>
      <td><a href="https://gitea.com/gitea/act_runner">Gitea act_runner</a></td>
      <td><ul>
        <li>Community effort for on-premise GitHub Actions compatibility.</li>
      </ul></td>
      <td><ul>
        <li>Requires a VM-like environment, as GitHub Actions does.</li>
        <li>No native Kubernetes integration.</li>
      </ul></td>
    </tr>
    <tr>
      <td><a href="https://github.com/jlsalvador/simple-cicd">Simple CI/CD</a></td>
      <td><ul>
        <li>Open-source.</li>
        <li>Kubernetes-native operator.</li>
        <li>Platform agnostic (cloud, on-premise, hybrid, air-gap).</li>
        <li>No external dependencies.</li>
        <li>Low resource usage (~8 MB RAM).</li>
      </ul></td>
      <td><ul>
        <li>Experimental; not yet battle-tested in production.</li>
        <li>Maintained by a single individual.</li>
        <li>No web UI (yet).</li>
      </ul></td>
    </tr>
  </tbody>
</table>
<!-- markdownlint-enable MD033 -->

---

## License

Copyright 2023 José Luis Salvador Rufo <salvador.joseluis@gmail.com>.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

<http://www.apache.org/licenses/LICENSE-2.0>

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

[Operator pattern]: https://kubernetes.io/docs/concepts/extend-kubernetes/operator/
