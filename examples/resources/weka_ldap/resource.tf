resource "weka_ldap" "this" {
  enabled                    = true
  server_uri                 = "ldaps://ldap.example.com"
  base_dn                    = "DC=example,DC=com"
  user_object_class          = "user"
  user_id_attribute          = "sAMAccountName"
  group_object_class         = "group"
  group_id_attribute         = "cn"
  group_membership_attribute = "member"
  reader_username            = "reader@example.com"
  reader_password            = "placeholder"
}
