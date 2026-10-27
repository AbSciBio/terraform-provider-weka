# Changelog

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Nothing has
been tagged yet; entries move under a version heading at the first release.

## [Unreleased]

### Added

- `weka_filesystem_group`, `weka_filesystem`, `weka_organization`, `weka_user`,
  `weka_object_store_bucket`, `weka_ldap`, and `weka_lockout_policy` resources.
- A matching data source for each resource above.
- GitHub Actions: unit tests on every push, acceptance tests by hand, and a GoReleaser release on `v*` tags.

### Changed

- Provider address is `registry.terraform.io/abscibio/weka`.
