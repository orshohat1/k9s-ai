# Security Skill

> RBAC auditing, security posture, and policy analysis for Kubernetes clusters.

Use these playbooks to assess and improve the security posture of the cluster.
Flag all findings with a severity level: **Critical**, **High**, **Medium**, or **Low**.

---

## RBAC Audit Checklist

1. **ClusterRoleBindings** — list all and check for overly permissive bindings
   - Flag any binding to `cluster-admin`
   - Flag wildcard (`*`) verbs or resources
   - Flag bindings to default service accounts
2. **ServiceAccounts** — check for unnecessary token mounts
   - `automountServiceAccountToken: false` should be default for most workloads
3. **Secrets access** — who can read secrets in each namespace
4. **Privilege escalation** — roles that can create/update roles or bindings

---

## Container Security Scan

1. Check pods for:
   - `runAsRoot: true` or missing `runAsNonRoot: true`
   - `privileged: true` in security context
   - Missing `readOnlyRootFilesystem`
   - `hostNetwork`, `hostPID`, `hostIPC` enabled
   - Capabilities beyond the minimum (check `drop: ["ALL"]`)
2. Check for missing NetworkPolicies in namespaces
3. Check for pods without resource limits (noisy neighbor risk)

---

## Network Policy Analysis

1. List NetworkPolicies in the namespace
2. Check if ingress/egress rules are appropriately scoped
3. Flag namespaces with no NetworkPolicies (all traffic allowed)
4. Verify pods are selected by at least one NetworkPolicy
