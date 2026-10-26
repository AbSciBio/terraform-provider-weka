resource "weka_user" "s3" {
  username  = "s3user"
  password  = "ChangeMe-1"
  role      = "S3"
  posix_uid = 1000
  posix_gid = 1000
}
