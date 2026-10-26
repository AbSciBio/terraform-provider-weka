package provider

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"gitlab.com/absci/devops/terraform-provider-weka/internal/wekaapi"
)

func TestLockoutPolicyResource(t *testing.T) {
	f := newFakeWeka(t)

	var mu sync.Mutex
	var policy *wekaapi.LockoutPolicy
	resets := 0

	f.Handle("GET /lockoutPolicy", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if policy == nil {
			writeError(w, http.StatusNotFound, "lockout policy not found")
			return
		}
		writeData(w, *policy)
	})
	f.Handle("PUT /lockoutPolicy", func(w http.ResponseWriter, r *http.Request) {
		var body wekaapi.LockoutPolicy
		decodeBody(t, r, &body)
		mu.Lock()
		policy = &body
		mu.Unlock()
		writeData(w, body)
	})
	// 404 is success: the spec documents no 404, and delete must still finish.
	f.Handle("DELETE /lockoutPolicy", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		resets++
		policy = nil
		mu.Unlock()
		writeError(w, http.StatusNotFound, "lockout policy not found")
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			if resets != 1 {
				return fmt.Errorf("resetLockoutPolicy called %d times, want 1", resets)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.config(`
resource "weka_lockout_policy" "test" {
  lockout_duration  = 120
  num_failed_logins = 5
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_lockout_policy.test", "id", "cluster"),
					resource.TestCheckResourceAttr("weka_lockout_policy.test", "lockout_duration", "120"),
					resource.TestCheckResourceAttr("weka_lockout_policy.test", "num_failed_logins", "5"),
				),
			},
			{
				Config: f.config(`
resource "weka_lockout_policy" "test" {
  lockout_duration  = 300
  num_failed_logins = 3
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_lockout_policy.test", "id", "cluster"),
					resource.TestCheckResourceAttr("weka_lockout_policy.test", "lockout_duration", "300"),
					resource.TestCheckResourceAttr("weka_lockout_policy.test", "num_failed_logins", "3"),
				),
			},
			{
				ResourceName:      "weka_lockout_policy.test",
				ImportState:       true,
				ImportStateId:     "cluster",
				ImportStateVerify: true,
			},
		},
	})
}
