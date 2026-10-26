resource "weka_filesystem_group" "tiered" {
  name                 = "tiered"
  target_ssd_retention = 86400
  start_demote         = 1800
}
