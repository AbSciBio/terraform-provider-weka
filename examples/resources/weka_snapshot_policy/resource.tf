resource "weka_snapshot_policy" "home" {
  name        = "home"
  filesystems = [weka_filesystem.home.id]

  daily = {
    enable    = true
    time      = "07:00"
    days      = "all"
    retention = 7
    upload    = "remote"
  }
}

resource "weka_snapshot_policy" "share" {
  name        = "share"
  filesystems = [weka_filesystem.share.id]

  daily = {
    enable    = true
    time      = "19:30"
    days      = "all"
    retention = 7
    upload    = "remote"
  }

  hourly = {
    enable        = true
    hours         = "all"
    days          = "all"
    minute_offset = 0
    retention     = 24
    upload        = "remote"
  }
}
