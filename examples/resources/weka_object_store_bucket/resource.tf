resource "weka_object_store_bucket" "cold" {
  name     = "cold"
  obs_name = "default"

  obs_site    = "local"
  obs_type    = "AWS"
  protocol    = "HTTPS"
  hostname    = "s3.example.com"
  port        = 443
  bucket      = "cold-data"
  auth_method = "AWSSignature4"
  region      = "us-east-1"

  access_key_id = "placeholder-access-key"
  secret_key    = "placeholder-secret-key"
}
