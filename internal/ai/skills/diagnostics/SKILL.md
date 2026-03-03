# Diagnostics Skill

> Root-cause analysis and remediation for unhealthy Kubernetes workloads.

Use these playbooks as step-by-step guides when diagnosing Kubernetes workloads.
Always follow the relevant playbook before suggesting fixes.

---

## CrashLoopBackOff

A pod is restarting repeatedly. Containers crash, restart, then crash again with exponential backoff.

**Steps:**
1. `get_pod_diagnostics` — check container states, restart count, exit codes
2. `get_logs` with `previous=true` — get the crash log from the last terminated container
3. `get_events` for the pod — look for Warning events
4. Check exit codes:
   - **Exit 1** → application error (bad config, missing env, startup failure)
   - **Exit 137** → killed by SIGKILL (OOM or preemption) — check resource limits
   - **Exit 139** → segfault — likely a bug in the application binary
   - **Exit 143** → killed by SIGTERM (graceful shutdown issue)
   - **Exit 0 with restarts** → container completes but restartPolicy=Always
5. Check if the container image exists and is pullable
6. Look for missing ConfigMaps, Secrets, or volume mounts in the describe output

**Common fixes:**
- Bad image → `patch_resource` to update image
- Missing env var → `patch_resource` to add env or mount ConfigMap
- OOMKilled → `patch_resource` to increase memory limits
- Bad command → `patch_resource` to fix container command/args

---

## OOMKilled

Container was killed because it exceeded memory limits.

**Steps:**
1. `get_pod_diagnostics` — look for `reason: OOMKilled` in container states
2. Check `lastTermination` for exit code 137
3. Compare `resourceLimits.memory` with actual usage patterns
4. Check if the application has known memory leaks
5. `get_events` — look for "OOMKilling" events

**Common fixes:**
- Increase memory limit via `patch_resource` (but warn about cost)
- If requests are much lower than limits, align them
- Suggest the user investigate the application for memory leaks if limits seem adequate

---

## ImagePullBackOff / ErrImagePull

Container image cannot be pulled.

**Steps:**
1. `get_pod_diagnostics` — check waiting reason
2. `get_events` — look for "Failed to pull image" messages with details
3. Common causes:
   - Image tag doesn't exist → typo in image name/tag
   - Private registry → missing or wrong imagePullSecrets
   - Registry rate limit → Docker Hub rate limiting
   - DNS resolution → can't resolve registry hostname

**Common fixes:**
- Wrong image/tag → `patch_resource` to fix image name
- Missing pull secret → inform user to create the secret, then patch imagePullSecrets

---

## Pending Pods (Scheduling Failures)

Pod stays in Pending state and is not scheduled to any node.

**Steps:**
1. `get_pod_diagnostics` — verify phase is Pending, check conditions
2. `get_events` — look for FailedScheduling events with reasons
3. `get_cluster_health` — check node readiness and capacity
4. Common scheduling failure reasons:
   - **Insufficient cpu/memory** → nodes don't have enough resources
   - **node(s) had taint** → pod doesn't tolerate node taints
   - **no nodes match selector** → nodeSelector or affinity doesn't match
   - **PVC not bound** → PersistentVolumeClaim pending

**Common fixes:**
- Resource over-request → `patch_resource` to lower resource requests
- Missing toleration → `patch_resource` to add tolerations
- Wrong node selector → `patch_resource` to fix nodeSelector
- Scaling issue → suggest cluster autoscaler or adding nodes

---

## CreateContainerConfigError

Container cannot start due to configuration issues.

**Steps:**
1. `get_pod_diagnostics` — check waiting reason
2. `get_events` — look for specific config error message
3. Common causes:
   - Referenced ConfigMap doesn't exist
   - Referenced Secret doesn't exist
   - Invalid security context
   - Invalid volume mount

**Common fixes:**
- Create the missing ConfigMap/Secret (tell user)
- Fix the reference name via `patch_resource`

---

## Fix Verification

After applying any mutation:
1. Wait a moment for the change to propagate
2. Re-fetch the resource to verify the change was applied
3. Check events for new activity (rollout started, new pods scheduled)
4. For deployment patches, check if new pods are Running
5. Report the before/after state to the user
