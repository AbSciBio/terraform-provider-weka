package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"gitlab.com/absci/devops/terraform-provider-weka/internal/wekaapi"
)

func TestAccOrganization(t *testing.T) {
	// resource.Test skips unless TF_ACC=1, so CI never touches a cluster.
	api := organizationAPI(t)
	name := accName()
	renamed := name + "b"
	username := accName()
	password := accOrgPassword()

	var baseline map[string]string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		PreCheck: func() {
			accPreCheck(t)
			baseline = organizationNames(t, api)
			for n, uid := range baseline {
				if strings.HasPrefix(n, accPrefix) {
					// A previous run died before cleanup. Only this prefix is ours.
					accDeleteOrganization(t, api, n, uid)
					delete(baseline, n)
				}
			}
		},
		CheckDestroy: func(s *terraform.State) error {
			after := organizationNames(t, api)
			for n := range baseline {
				if _, ok := after[n]; !ok {
					return fmt.Errorf("pre-existing organization %q is gone", n)
				}
			}
			for n := range after {
				if strings.HasPrefix(n, accPrefix) {
					return fmt.Errorf("temporary organization %q was not removed", n)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				// Multiples of 8192 that float32 JSON sends unchanged. 1 GiB encodes low
				// and 2147488000 is stored by the cluster as 2147491840.
				Config: organizationConfig(name, username, password, "ssd_quota = 1074790400\n  total_quota = 2147532800"),
				Check: resource.ComposeAggregateTestCheckFunc(
					accOrganizationMustBeOurs(name),
					resource.TestCheckResourceAttr("weka_organization.tfacc-org", "name", name),
					resource.TestCheckResourceAttr("weka_organization.tfacc-org", "username", username),
					resource.TestCheckResourceAttr("weka_organization.tfacc-org", "ssd_quota", "1074790400"),
					resource.TestCheckResourceAttr("weka_organization.tfacc-org", "total_quota", "2147532800"),
					resource.TestCheckResourceAttrSet("weka_organization.tfacc-org", "id"),
				),
			},
			{
				Config: organizationConfig(renamed, username, password, "ssd_quota = 2147532800\n  total_quota = 4295065600"),
				Check: resource.ComposeAggregateTestCheckFunc(
					accOrganizationMustBeOurs(renamed),
					resource.TestCheckResourceAttr("weka_organization.tfacc-org", "name", renamed),
					resource.TestCheckResourceAttr("weka_organization.tfacc-org", "ssd_quota", "2147532800"),
					resource.TestCheckResourceAttr("weka_organization.tfacc-org", "total_quota", "4295065600"),
				),
			},
			{
				ResourceName:            "weka_organization.tfacc-org",
				ImportState:             true,
				ImportStateId:           renamed,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"username", "password"},
			},
		},
	})
}

func organizationAPI(t *testing.T) *wekaapi.API {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		return nil
	}
	insecure, _ := strconv.ParseBool(os.Getenv("WEKA_INSECURE"))
	api, err := wekaapi.New(wekaapi.Config{
		Endpoint:           os.Getenv("WEKA_ENDPOINT"),
		Org:                os.Getenv("WEKA_ORG"),
		Username:           os.Getenv("WEKA_USERNAME"),
		Password:           os.Getenv("WEKA_PASSWORD"),
		InsecureSkipVerify: insecure,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sweepOrganizations(t, api) })
	return api
}

func organizationConfig(name, username, password, attrs string) string {
	if !strings.HasPrefix(name, accPrefix) || !strings.HasPrefix(username, accPrefix) {
		panic("acceptance config name " + name + " or username " + username + " is outside " + accPrefix)
	}
	return fmt.Sprintf(`
provider "weka" {}

resource "weka_organization" "tfacc-org" {
  name     = %q
  username = %q
  password = %q
  %s
}
`, name, username, password, attrs)
}

func accOrganizationMustBeOurs(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources["weka_organization.tfacc-org"]
		if !ok {
			return fmt.Errorf("weka_organization.tfacc-org missing from state")
		}
		if rs.Primary.Attributes["name"] != name || !strings.HasPrefix(name, accPrefix) {
			return fmt.Errorf("refusing to keep going: resource name %q is not %q", rs.Primary.Attributes["name"], name)
		}
		return nil
	}
}

func accOrgPassword() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "Aa1-" + hex.EncodeToString(b[:])
}

func organizationNames(t *testing.T, api *wekaapi.API) map[string]string {
	t.Helper()
	out, err := api.GetOrganizationsWithResponse(context.Background())
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	if out.JSON200 == nil || out.JSON200.Data == nil {
		return names
	}
	for _, o := range *out.JSON200.Data {
		names[deref(o.Name)] = deref(o.Uid)
	}
	return names
}

// accDeleteOrganization removes one organization. It refuses any name outside accPrefix.
func accDeleteOrganization(t *testing.T, api *wekaapi.API, name, uid string) {
	t.Helper()
	if !strings.HasPrefix(name, accPrefix) || uid == "" {
		t.Fatalf("refusing to delete organization %q (%s)", name, uid)
	}
	out, err := api.DeleteOrganizationWithResponse(context.Background(), uid)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		t.Fatal(err)
	}
}

// sweepOrganizations removes whatever this run left behind. It never touches other names.
func sweepOrganizations(t *testing.T, api *wekaapi.API) {
	t.Helper()
	for name, uid := range organizationNames(t, api) {
		if strings.HasPrefix(name, accPrefix) {
			accDeleteOrganization(t, api, name, uid)
		}
	}
}
