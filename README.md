# terraform-weka

Terraform provider for configuring a running Weka cluster through its REST API.

Private for now. Intended to be published later, so keep cluster-specific names, credentials, and endpoints out of this repo.

## Scope

A Weka Kubernetes operator owns the cluster: backends, clients, drives, CSI, and whether protocol services exist.

This provider owns configuration inside a cluster that is already up:

- filesystems and filesystem groups
- quotas and snapshot policies
- object-store buckets
- S3 buckets and policies
- NFS permissions and SMB shares
- organizations, users, and login policy

## Layout

- `internal/wekaapi/` — Go client generated from a pinned Weka 4.4 OpenAPI spec, plus a wrapper for token refresh and calls that finish in the background.
- Provider resources — hand-written on top of that client.

Neither exists yet.

The public provider source will be something like `registry.terraform.io/<namespace>/weka`. The repo can be renamed to `terraform-provider-weka` at publish time; the registry protocol expects that name.

## License

[Mozilla Public License 2.0](LICENSE), the usual license for Terraform providers.
