# terraform-provider-weka

Terraform provider for configuring a running Weka cluster through its REST API.

A Weka Kubernetes operator owns cluster layout: backends, clients, drives, CSI, and protocol services. This provider configures a cluster that is already running.

## Usage

```hcl
terraform {
  required_providers {
    weka = {
      source  = "abscibio/weka"
      version = "~> 0.1"
    }
  }
}

provider "weka" {
  endpoint = "https://weka.example.com:14000"
  username = var.weka_username
  password = var.weka_password
}
```

## Resources

Each resource has a matching data source.

- `weka_filesystem_group` and `weka_filesystem`
- `weka_organization` and `weka_user`
- `weka_object_store_bucket`
- `weka_ldap` and `weka_lockout_policy`

## Development

Build the binary and point Terraform at it with [`dev_overrides`](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers). `terraform init` is not required while an override is set, and Terraform warns on every plan and apply.

```shell
go build -o ~/go/bin/terraform-provider-weka .
```

```hcl
# ~/.terraformrc
provider_installation {
  dev_overrides {
    "registry.terraform.io/abscibio/weka" = "/Users/you/go/bin"
  }
  direct {}
}
```

Contributor conventions are in [AGENTS.md](AGENTS.md).

## License

[Mozilla Public License 2.0](LICENSE), the usual license for Terraform providers.
