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
- Second counts (`target_ssd_retention`, `start_demote`) are `float32` on the wire. Every integer is exact through `2^24` (about 194 days); above that only some are. Use `exactSeconds()`, which rejects a value `float32` would change (16777217 becomes 16777216). Whole days stay exact well past that: 360 days (`31104000`) and 1080 days (`93312000`) are valid. Keep `int64validator.Between(0, maxExactSeconds)` for counts where every integer matters and real values are small (quotas, UIDs, failed-login limits).
- `updateFileSystemGroup` and `deleteFileSystemGroup` document only `400`/`401` in the spec — no `404`, unlike `getFileSystemGroup`. Confirm the real 404 behavior on PUT/DELETE before relying on `wekaapi.IsNotFound` there for another resource.
- `resolvePath` types `inode_id` as a string but shows a number. Check the wire format before relying on it.
- A filesystem group with only one of `target_ssd_retention` / `start_demote` is rejected (`HTTP 400: must specify both demotion and retention time`). Omit both, or set both. When both are set, `start_demote` must be at most `target_ssd_retention / 3`. `ValidateConfig` enforces both.
- Not yet checked against a real cluster: 404 (rather than 400) for an unknown UID on delete.
