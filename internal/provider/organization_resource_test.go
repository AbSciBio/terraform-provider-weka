package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"gitlab.com/absci/devops/terraform-provider-weka/internal/wekaapi"
)

// fakeOrganizations adds /organizations to f and returns its store.
func fakeOrganizations(t *testing.T, f *fakeWeka) *sync.Map {
	var store sync.Map
	var mu sync.Mutex
	next := 0

	f.Handle("POST /organizations", func(w http.ResponseWriter, r *http.Request) {
		var body wekaapi.CreateOrganizationJSONBody
		decodeBody(t, r, &body)
		mu.Lock()
		next++
		uid := fmt.Sprintf("uid-%d", next)
		mu.Unlock()
		o := wekaapi.Organization{Uid: &uid, Name: &body.Name, SsdQuota: body.SsdQuota, TotalQuota: body.TotalQuota}
		store.Store(uid, o)
		writeData(w, o)
	})
	f.Handle("GET /organizations", func(w http.ResponseWriter, r *http.Request) {
		var all []wekaapi.Organization
		store.Range(func(_, v any) bool { all = append(all, v.(wekaapi.Organization)); return true })
		writeData(w, all)
	})
	f.Handle("GET /organizations/{uid}", func(w http.ResponseWriter, r *http.Request) {
		o, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "organization not found")
			return
		}
		writeData(w, o)
	})
	f.Handle("PUT /organizations/{uid}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "organization not found")
			return
		}
		o := v.(wekaapi.Organization)
		var body wekaapi.UpdateOrganizationJSONBody
		decodeBody(t, r, &body)
		if body.NewName != nil {
			o.Name = body.NewName
		}
		store.Store(*o.Uid, o)
		writeData(w, o)
	})
	f.Handle("PUT /organizations/{uid}/limits", func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "organization not found")
			return
		}
		o := v.(wekaapi.Organization)
		var body wekaapi.SetOrganizationLimitJSONBody
		decodeBody(t, r, &body)
		if body.SsdQuota != nil {
			o.SsdQuota = body.SsdQuota
		}
		if body.TotalQuota != nil {
			o.TotalQuota = body.TotalQuota
		}
		store.Store(*o.Uid, o)
		writeData(w, o)
	})
	f.Handle("DELETE /organizations/{uid}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := store.LoadAndDelete(r.PathValue("uid")); !ok {
			writeError(w, http.StatusNotFound, "organization not found")
			return
		}
		writeData(w, "ok")
	})
	return &store
}

func TestOrganizationResource(t *testing.T) {
	f := newFakeWeka(t)
	store := fakeOrganizations(t, f)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			left := 0
			store.Range(func(_, _ any) bool { left++; return true })
			if left != 0 {
				return fmt.Errorf("%d organizations left after destroy", left)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.config(`
resource "weka_organization" "test" {
  name        = "org"
  username    = "org-admin"
  password    = "placeholder"
  ssd_quota   = 1024
  total_quota = 2048
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_organization.test", "id", "uid-1"),
					resource.TestCheckResourceAttr("weka_organization.test", "name", "org"),
					resource.TestCheckResourceAttr("weka_organization.test", "username", "org-admin"),
					resource.TestCheckResourceAttr("weka_organization.test", "password", "placeholder"),
					resource.TestCheckResourceAttr("weka_organization.test", "ssd_quota", "1024"),
					resource.TestCheckResourceAttr("weka_organization.test", "total_quota", "2048"),
				),
			},
			{
				Config: f.config(`
resource "weka_organization" "test" {
  name        = "org2"
  username    = "org-admin"
  password    = "placeholder"
  ssd_quota   = 1024
  total_quota = 2048
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_organization.test", "id", "uid-1"),
					resource.TestCheckResourceAttr("weka_organization.test", "name", "org2"),
					resource.TestCheckResourceAttr("weka_organization.test", "ssd_quota", "1024"),
					resource.TestCheckResourceAttr("weka_organization.test", "total_quota", "2048"),
				),
			},
			{
				Config: f.config(`
resource "weka_organization" "test" {
  name        = "org2"
  username    = "org-admin"
  password    = "placeholder"
  ssd_quota   = 4096
  total_quota = 8192
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_organization.test", "id", "uid-1"),
					resource.TestCheckResourceAttr("weka_organization.test", "name", "org2"),
					resource.TestCheckResourceAttr("weka_organization.test", "ssd_quota", "4096"),
					resource.TestCheckResourceAttr("weka_organization.test", "total_quota", "8192"),
				),
			},
			{
				ResourceName:            "weka_organization.test",
				ImportState:             true,
				ImportStateId:           "org2",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"username", "password"},
			},
			{
				ResourceName:            "weka_organization.test",
				ImportState:             true,
				ImportStateId:           "uid-1",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"username", "password"},
			},
			{
				// Import leaves username and password null. Applying the same
				// config records them and must not create a second organization.
				Config: f.config(`
resource "weka_organization" "test" {
  name        = "org2"
  username    = "org-admin"
  password    = "placeholder"
  ssd_quota   = 4096
  total_quota = 8192
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_organization.test", "id", "uid-1"),
					resource.TestCheckResourceAttr("weka_organization.test", "username", "org-admin"),
					resource.TestCheckResourceAttr("weka_organization.test", "password", "placeholder"),
				),
			},
			{
				// 2^24 + 1: the first value a float32 cannot represent exactly.
				Config: f.config(`
resource "weka_organization" "test" {
  name        = "org2"
  username    = "org-admin"
  password    = "placeholder"
  ssd_quota   = 16777217
  total_quota = 8192
}`),
				ExpectError: regexp.MustCompile(`(?s)Attribute ssd_quota.*between 0 and 16777216`),
			},
			{
				// Deleted outside Terraform: Read drops it and the plan recreates it.
				PreConfig: func() { store.Delete("uid-1") },
				Config: f.config(`
resource "weka_organization" "test" {
  name        = "org2"
  username    = "org-admin"
  password    = "placeholder"
  ssd_quota   = 4096
  total_quota = 8192
}`),
				Check: resource.TestCheckResourceAttr("weka_organization.test", "id", "uid-2"),
			},
		},
	})
}
