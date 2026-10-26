resource "weka_organization" "example" {
  name        = "example"
  username    = "example-admin"
  password    = "placeholder"
  ssd_quota   = 1048576
  total_quota = 2097152
}
