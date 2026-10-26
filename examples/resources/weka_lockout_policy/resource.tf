resource "weka_lockout_policy" "this" {
  lockout_duration  = 120
  num_failed_logins = 5
}
