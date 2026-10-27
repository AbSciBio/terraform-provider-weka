# Changelog

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Nothing has
been tagged yet; entries move under a version heading at the first release.

## [Unreleased]

### Added

- `weka_filesystem_group`, `weka_filesystem`, `weka_organization`, `weka_user`,
  `weka_object_store_bucket`, `weka_ldap`, and `weka_lockout_policy` resources.
- A matching data source for each resource above.
- GitHub Actions: unit tests on every push. A push to the default branch tags `feat` and `fix` commits and publishes the binaries.

### Changed

- Provider address is `registry.terraform.io/abscibio/weka`.
- Module path is `github.com/AbSciBio/terraform-provider-weka`.
