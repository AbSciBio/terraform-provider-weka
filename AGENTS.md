# terraform-provider-weka

Terraform provider for configuration inside a running Weka cluster (filesystems, quotas, snapshot policies, S3, users). The Weka operator owns cluster layout; do not add resources for containers, drives, hosts, failure domains, KMS, or turning protocol services on.

This repo will be published. No cluster names, endpoints, credentials, or internal hostnames in code, tests, examples, or docs.

## Commands

```bash
go generate ./...                 # regenerate the API client and docs/
go vet ./... && go test -race ./...

# Real cluster. Skipped unless TF_ACC=1. Creates only names starting with
# tfacc-, deletes them afterwards, and fails if any other filesystem group
# disappeared. Set WEKA_ENDPOINT, WEKA_USERNAME, WEKA_PASSWORD, and
# WEKA_INSECURE=true. Do not point this at a production cluster.
TF_ACC=1 go test -count=1 -timeout 15m -run TestAcc ./internal/provider/
```

Commit the output of `go generate`. CI fails if it is stale.

GitHub Actions runs `go generate` / `go vet` / `go test -race` on every push. Acceptance tests are `workflow_dispatch` only. A push to `main` or `feat/provider-scaffold` runs semantic-release, which tags `feat` and `fix` commits, then GoReleaser publishes the binaries in that same job. That job needs the `GPG_PRIVATE_KEY` and `PASSPHRASE` secrets.

## Layout

| Path | What |
|---|---|
| `api/weka-4.4.json` | Pinned Weka 4.4 OpenAPI spec. Never edit; fix generation problems in `internal/wekaapi/overlay.yaml`. |
| `internal/wekaapi/client.gen.go` | Generated. Never edit. Tags included are listed in `oapi-codegen.yaml`. |
| `internal/wekaapi/wrapper.go` | `API` (login and re-login), `Check`, `IsNotFound`, `Poll`. |
| `internal/provider/filesystem_group_resource.go` | Reference resource. Copy it. |
| `internal/provider/fake_weka_test.go` | Fake API for unit tests. |
| `examples/` | Feeds `docs/` through `tfplugindocs`. |

## Adding a resource

Copy `filesystem_group_resource.go` and its test. Each resource:

- Lives in `internal/provider/<name>_resource.go` with `<name>_resource_test.go`, and gets one line in `Resources()` in `provider.go`.
- Calls `wekaapi.Check(resp.StatusCode(), resp.Body)` after every generated call.
- On Read, a 404 (`wekaapi.IsNotFound`) means `resp.State.RemoveResource`. On Delete, a 404 is success.
- Implements import by UID **and** by name: list, then match either. Existing clusters are adopted by name.
- Uses `Optional + Computed + UseStateForUnknown` for anything the server defaults.
- Uses `wekaapi.Poll` when the API returns before the change has finished, and fails rather than guessing if it never settles.
- Has a unit test against `newFakeWeka`: create, update, import, and a deletion done outside Terraform. Register the fake's handlers in the test file; do not grow `fake_weka_test.go` per resource.
- Has `examples/resources/weka_<name>/resource.tf` and `import.sh`.

Cross-resource references are plain UID strings. Do not reach into another resource's code.

## Spec quirks found so far

- `/fileSystems/{inode_id}/getPath` and `/resolve` share an `operationId`; the overlay renames the first.
- Login `data` is an object in the spec and an array in the docs example. `parseTokens` accepts both.
- Second counts (`target_ssd_retention`, `start_demote`) and organization quotas are `float32` on the wire. Every integer is exact through `2^24`; above that only some are. Use `exactSeconds()`, which rejects a value `float32` would change (16777217 becomes 16777216). Whole days stay exact well past that. `encoding/json` can still emit a different decimal for an exact float32: 1 GiB goes out as 1073741800. Organization quotas are also rounded up to a multiple of 8192 by the cluster. `quotaWire` picks a value that survives both. Keep `int64validator.Between(0, maxExactSeconds)` for UIDs and failed-login limits, where every integer matters and real values are small.
- `authDoer.Do` in `internal/wekaapi/wrapper.go` logs every request and response with `tflog`, including re-login on a 401. Never add a field with the password or token; org, method, path, and status only.
- Every resource has a matching data source (e.g. `weka_filesystem_group` resource and data source), looked up by `id` or `name` (`weka_user` uses `username` in place of `name`). `ConfigValidators` enforces exactly one. LDAP and lockout policy are singletons, so their data sources take no lookup key. Each data source reuses its resource's private `get`/`list`/`...FromAPI` functions instead of duplicating the HTTP calls; for the two singletons it even instantiates the resource struct (`(&ldapResource{api: d.api}).get(...)`) since both live in the same package.
- A non-tiered filesystem rejects an explicit `ssd_capacity`. `ssdPtr` omits it when the configuration leaves it unset or sets it equal to `total_capacity`. Use the configuration value, not the plan. `ssdFollowsTotal` plans the omitted value as the new total, because the cluster rewrites SSD to match total and `UseStateForUnknown` would keep the old one. Unequal SSD capacity requires an object store attached to the filesystem. Retention and demote on the group do not make it tiered.
- `updateFileSystemGroup` and `deleteFileSystemGroup` document only `400`/`401` in the spec — no `404`, unlike `getFileSystemGroup`. Confirm the real 404 behavior on PUT/DELETE before relying on `wekaapi.IsNotFound` there for another resource.
- `resolvePath` types `inode_id` as a string but shows a number. Check the wire format before relying on it.
- A filesystem group with only one of `target_ssd_retention` / `start_demote` is rejected (`HTTP 400: must specify both demotion and retention time`). Omit both, or set both. When both are set, `start_demote` must be at most `target_ssd_retention / 3`. `ValidateConfig` enforces both.
- Not yet checked against a real cluster: 404 (rather than 400) for an unknown UID on delete.
