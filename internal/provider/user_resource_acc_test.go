package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/AbSciBio/terraform-provider-weka/internal/wekaapi"
)

func TestAccUser(t *testing.T) {
	// resource.Test skips unless TF_ACC=1, so CI never touches a cluster.
	api := accAPI(t)
	if api != nil {
		t.Cleanup(func() { userSweep(t, api) })
	}
	name := accName()

	var baseline map[string]string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		PreCheck: func() {
			accPreCheck(t)
			baseline = userNames(t, api)
			for n, uid := range baseline {
				if strings.HasPrefix(n, accPrefix) {
					// A previous run died before cleanup. Only this prefix is ours.
					userDelete(t, api, n, uid)
					delete(baseline, n)
				}
			}
		},
		CheckDestroy: func(s *terraform.State) error {
			after := userNames(t, api)
			for n := range baseline {
				if _, ok := after[n]; !ok {
					return fmt.Errorf("pre-existing user %q is gone", n)
				}
			}
			for n := range after {
				if strings.HasPrefix(n, accPrefix) {
					return fmt.Errorf("temporary user %q was not removed", n)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: userAccConfig(name, `role = "Regular"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					userMustBeOurs(name),
					resource.TestCheckResourceAttr("weka_user.test", "username", name),
					resource.TestCheckResourceAttr("weka_user.test", "role", "Regular"),
					resource.TestCheckResourceAttrSet("weka_user.test", "id"),
				),
			},
			{
				Config: userAccConfig(name, `role = "ReadOnly"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					userMustBeOurs(name),
					resource.TestCheckResourceAttr("weka_user.test", "username", name),
					resource.TestCheckResourceAttr("weka_user.test", "role", "ReadOnly"),
				),
			},
			{
				ResourceName:            "weka_user.test",
				ImportState:             true,
				ImportStateId:           name,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password"},
			},
		},
	})
}

func userAccConfig(name, attrs string) string {
	if !strings.HasPrefix(name, accPrefix) {
		panic("acceptance config name " + name + " is outside " + accPrefix)
	}
	return fmt.Sprintf(`
provider "weka" {}

resource "weka_user" "test" {
  username = %q
  password = "Tfacc-placeholder-1"
  %s
}
`, name, attrs)
}

func userMustBeOurs(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources["weka_user.test"]
		if !ok {
			return fmt.Errorf("weka_user.test missing from state")
		}
		if rs.Primary.Attributes["username"] != name || !strings.HasPrefix(name, accPrefix) {
			return fmt.Errorf("refusing to keep going: resource username %q is not %q", rs.Primary.Attributes["username"], name)
		}
		return nil
	}
}

func userNames(t *testing.T, api *wekaapi.API) map[string]string {
	t.Helper()
	out, err := api.GetUsersWithResponse(context.Background())
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
	for _, u := range *out.JSON200.Data {
		names[deref(u.Username)] = deref(u.Uid)
	}
	return names
}

// userDelete removes one user. It refuses any name outside accPrefix.
func userDelete(t *testing.T, api *wekaapi.API, name, uid string) {
	t.Helper()
	if !strings.HasPrefix(name, accPrefix) || uid == "" {
		t.Fatalf("refusing to delete user %q (%s)", name, uid)
	}
	out, err := api.DeleteUserWithResponse(context.Background(), uid)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		t.Fatal(err)
	}
}

// userSweep removes whatever this run left behind. It never touches other names.
func userSweep(t *testing.T, api *wekaapi.API) {
	t.Helper()
	for name, uid := range userNames(t, api) {
		if strings.HasPrefix(name, accPrefix) {
			userDelete(t, api, name, uid)
		}
	}
}
