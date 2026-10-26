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

// ldapView is the GET /ldap body. reader_password is intentionally absent.
type ldapView struct {
	Enabled                  *bool   `json:"enabled,omitempty"`
	ServerURI                *string `json:"server_uri,omitempty"`
	BaseDN                   *string `json:"base_dn,omitempty"`
	UserObjectClass          *string `json:"user_object_class,omitempty"`
	UserIDAttribute          *string `json:"user_id_attribute,omitempty"`
	GroupObjectClass         *string `json:"group_object_class,omitempty"`
	GroupIDAttribute         *string `json:"group_id_attribute,omitempty"`
	GroupMembershipAttribute *string `json:"group_membership_attribute,omitempty"`
	ReaderUsername           *string `json:"reader_username,omitempty"`
}

type ldapStore struct {
	mu                 sync.Mutex
	present            bool
	forceDeleteMissing bool
	cfg                ldapView
}

func (s *ldapStore) clear() {
	s.mu.Lock()
	s.present = false
	s.cfg = ldapView{}
	s.mu.Unlock()
}

func (s *ldapStore) deleteMissing() {
	s.mu.Lock()
	s.forceDeleteMissing = true
	s.mu.Unlock()
}

func fakeLdap(t *testing.T, f *fakeWeka) *ldapStore {
	s := &ldapStore{}
	f.Handle("GET /ldap", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.present {
			writeError(w, http.StatusNotFound, "ldap not configured")
			return
		}
		writeData(w, s.cfg)
	})
	f.Handle("PUT /ldap", func(w http.ResponseWriter, r *http.Request) {
		var body wekaapi.UpdateLdapJSONBody
		decodeBody(t, r, &body)
		if body.ReaderPassword == nil || *body.ReaderPassword == "" {
			t.Errorf("PUT /ldap missing reader_password")
		}
		s.mu.Lock()
		s.present = true
		s.cfg = ldapView{
			Enabled:                  body.Enabled,
			ServerURI:                body.ServerUri,
			BaseDN:                   body.BaseDn,
			UserObjectClass:          body.UserObjectClass,
			UserIDAttribute:          body.UserIdAttribute,
			GroupObjectClass:         body.GroupObjectClass,
			GroupIDAttribute:         body.GroupIdAttribute,
			GroupMembershipAttribute: body.GroupMembershipAttribute,
			ReaderUsername:           body.ReaderUsername,
		}
		s.mu.Unlock()
		writeData(w, "ok")
	})
	f.Handle("DELETE /ldap", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.forceDeleteMissing || !s.present {
			s.present = false
			s.cfg = ldapView{}
			writeError(w, http.StatusNotFound, "ldap not configured")
			return
		}
		s.present = false
		s.cfg = ldapView{}
		writeData(w, "ok")
	})
	return s
}

func ldapConfig(serverURI, password string) string {
	return fmt.Sprintf(`
resource "weka_ldap" "this" {
  enabled                    = true
  server_uri                 = %q
  base_dn                    = "DC=example,DC=com"
  user_object_class          = "user"
  user_id_attribute          = "sAMAccountName"
  group_object_class         = "group"
  group_id_attribute         = "cn"
  group_membership_attribute = "member"
  reader_username            = "reader@example.com"
  reader_password            = %q
}
`, serverURI, password)
}

func TestLdapResource(t *testing.T) {
	f := newFakeWeka(t)
	store := fakeLdap(t, f)
	created := ldapConfig("ldaps://ldap.example.com", "placeholder")
	updated := ldapConfig("ldaps://ldap2.example.com", "rotated")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			store.mu.Lock()
			defer store.mu.Unlock()
			if store.present {
				return fmt.Errorf("ldap config still present after destroy")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.config(created),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_ldap.this", "id", "cluster"),
					resource.TestCheckResourceAttr("weka_ldap.this", "enabled", "true"),
					resource.TestCheckResourceAttr("weka_ldap.this", "server_uri", "ldaps://ldap.example.com"),
					resource.TestCheckResourceAttr("weka_ldap.this", "base_dn", "DC=example,DC=com"),
					resource.TestCheckResourceAttr("weka_ldap.this", "user_object_class", "user"),
					resource.TestCheckResourceAttr("weka_ldap.this", "user_id_attribute", "sAMAccountName"),
					resource.TestCheckResourceAttr("weka_ldap.this", "group_object_class", "group"),
					resource.TestCheckResourceAttr("weka_ldap.this", "group_id_attribute", "cn"),
					resource.TestCheckResourceAttr("weka_ldap.this", "group_membership_attribute", "member"),
					resource.TestCheckResourceAttr("weka_ldap.this", "reader_username", "reader@example.com"),
					resource.TestCheckResourceAttr("weka_ldap.this", "reader_password", "placeholder"),
				),
			},
			{
				// GET omits reader_password. A refresh must not plan a change.
				Config:             f.config(created),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
			{
				Config: f.config(updated),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_ldap.this", "id", "cluster"),
					resource.TestCheckResourceAttr("weka_ldap.this", "server_uri", "ldaps://ldap2.example.com"),
					resource.TestCheckResourceAttr("weka_ldap.this", "reader_password", "rotated"),
				),
			},
			{
				ResourceName:            "weka_ldap.this",
				ImportState:             true,
				ImportStateId:           "cluster",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"reader_password"},
			},
			{
				ResourceName:  "weka_ldap.this",
				ImportState:   true,
				ImportStateId: "other",
				ExpectError:   regexp.MustCompile(`import id must be "cluster"`),
			},
			{
				// Deleted outside Terraform: Read drops it and the plan recreates it.
				PreConfig: store.clear,
				Config:    f.config(updated),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_ldap.this", "id", "cluster"),
					resource.TestCheckResourceAttr("weka_ldap.this", "server_uri", "ldaps://ldap2.example.com"),
					resource.TestCheckResourceAttr("weka_ldap.this", "reader_password", "rotated"),
				),
			},
			{
				// Destroy calls deleteLdap. A 404 is success.
				PreConfig:          store.deleteMissing,
				Config:             f.config(updated),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
		},
	})
}
