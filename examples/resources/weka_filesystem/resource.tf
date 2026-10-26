resource "weka_filesystem" "data" {
  name           = "data"
  group_name     = "default"
  total_capacity = 1073741824
  ssd_capacity   = 1073741824
}
