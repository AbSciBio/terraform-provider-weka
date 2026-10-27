package provider

import (
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/AbSciBio/terraform-provider-weka/internal/wekaapi"
)

func TestFileSystemGroupDataSource(t *testing.T) {
	f := newFakeWeka(t)
	fakeFileSystemGroups(t, f)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.config(`
resource "weka_filesystem_group" "test" {
  name = "tier"
}

data "weka_filesystem_group" "found" {
  name = weka_filesystem_group.test.name
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("data.weka_filesystem_group.found", "id", "weka_filesystem_group.test", "id"),
					resource.TestCheckResourceAttr("data.weka_filesystem_group.found", "target_ssd_retention", "86400"),
					resource.TestCheckResourceAttr("data.weka_filesystem_group.found", "start_demote", "1800"),
				),
			},
		},
	})
}

func TestFileSystemDataSource(t *testing.T) {
	f := newFakeWeka(t)
	fakeFileSystems(t, f)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.config(`
resource "weka_filesystem" "test" {
  name           = "data"
  group_name     = "tier"
  total_capacity = 1073741824
}

data "weka_filesystem" "found" {
  id = weka_filesystem.test.id
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.weka_filesystem.found", "name", "data"),
					resource.TestCheckResourceAttr("data.weka_filesystem.found", "total_capacity", "1073741824"),
					resource.TestCheckResourceAttr("data.weka_filesystem.found", "ssd_capacity", "1073741824"),
				),
			},
		},
	})
}

func TestOrganizationDataSource(t *testing.T) {
	f := newFakeWeka(t)
	fakeOrganizations(t, f)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.config(`
resource "weka_organization" "test" {
  name     = "acme"
  username = "admin"
  password = "secret"
}

data "weka_organization" "found" {
  name = weka_organization.test.name
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("data.weka_organization.found", "id", "weka_organization.test", "id"),
					// The API never returns username/password; the data source leaves them null.
					resource.TestCheckNoResourceAttr("data.weka_organization.found", "username"),
					resource.TestCheckNoResourceAttr("data.weka_organization.found", "password"),
				),
			},
		},
	})
}

func TestUserDataSource(t *testing.T) {
	f := newFakeWeka(t)
	fakeUsers(t, f)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.config(`
resource "weka_user" "test" {
  username = "bob"
  password = "secret"
  role     = "ReadOnly"
}

data "weka_user" "found" {
  username = weka_user.test.username
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("data.weka_user.found", "id", "weka_user.test", "id"),
					resource.TestCheckResourceAttr("data.weka_user.found", "role", "ReadOnly"),
					resource.TestCheckNoResourceAttr("data.weka_user.found", "password"),
				),
			},
		},
	})
}

func TestObjectStoreBucketDataSource(t *testing.T) {
	f := newFakeWeka(t)
	fakeObsBuckets(t, f)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.config(`
resource "weka_object_store_bucket" "test" {
  name     = "bucket"
  obs_name = "default"
}

data "weka_object_store_bucket" "found" {
  id = weka_object_store_bucket.test.id
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.weka_object_store_bucket.found", "name", "bucket"),
					resource.TestCheckResourceAttr("data.weka_object_store_bucket.found", "obs_name", "default"),
				),
			},
		},
	})
}

func TestLdapDataSource(t *testing.T) {
	f := newFakeWeka(t)
	s := fakeLdap(t, f)
	s.mu.Lock()
	s.present = true
	s.cfg = ldapView{
		Enabled:   boolp(true),
		ServerURI: strp("ldap://dir.example.com"),
		BaseDN:    strp("dc=example,dc=com"),
	}
	s.mu.Unlock()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.config(`data "weka_ldap" "found" {}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.weka_ldap.found", "enabled", "true"),
					resource.TestCheckResourceAttr("data.weka_ldap.found", "server_uri", "ldap://dir.example.com"),
					resource.TestCheckNoResourceAttr("data.weka_ldap.found", "reader_password"),
				),
			},
		},
	})
}

func TestLockoutPolicyDataSource(t *testing.T) {
	f := newFakeWeka(t)
	f32 := func(v float32) *float32 { return &v }
	policy := wekaapi.LockoutPolicy{LockoutDuration: f32(120), NumFailedLogins: f32(5)}
	f.Handle("GET /lockoutPolicy", func(w http.ResponseWriter, r *http.Request) {
		writeData(w, policy)
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.config(`data "weka_lockout_policy" "found" {}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.weka_lockout_policy.found", "lockout_duration", "120"),
					resource.TestCheckResourceAttr("data.weka_lockout_policy.found", "num_failed_logins", "5"),
				),
			},
		},
	})
}

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }
