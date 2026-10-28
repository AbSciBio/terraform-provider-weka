resource "weka_object_store_bucket" "tiering" {
  name          = "tiering"
  obs_name      = "tiering"
  obs_site      = "local"
  hostname      = "s3.example.com"
  bucket        = "weka-tiering"
  auth_method   = "AWSSignature4"
  access_key_id = "placeholder-access-key"
  secret_key    = "placeholder-secret-key"
}

resource "weka_object_store_attachment" "tiering" {
  filesystem_id          = weka_filesystem.home.id
  object_store_bucket_id = weka_object_store_bucket.tiering.id
}
