package provider

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/AbSciBio/terraform-provider-weka/internal/wekaapi"
)

type storedUser struct {
	user     wekaapi.User
	password string
}

type userCalls struct {
	mu    sync.Mutex
	paths []string
}

func (c *userCalls) add(r *http.Request) {
	c.mu.Lock()
	c.paths = append(c.paths, r.Method+" "+r.URL.Path)
	c.mu.Unlock()
}

func (c *userCalls) n() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.paths)
}

func (c *userCalls) after(n int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.paths)-n)
	copy(out, c.paths[n:])
	return out
}

// fakeUsers adds /users to f. GET returns the user without a password.
// Role defaults to Regular when create omits it. That default is the fake's,
// not the provider's.
func fakeUsers(t *testing.T, f *fakeWeka) (*sync.Map, *userCalls) {
	t.Helper()
	var store sync.Map
	var mu sync.Mutex
	var calls userCalls
	next := 0
	str := func(v string) *string { return &v }

	f.Handle("POST /users", func(w http.ResponseWriter, r *http.Request) {
		calls.add(r)
		var body wekaapi.CreateUserJSONBody
		decodeBody(t, r, &body)
		mu.Lock()
		next++
		uid := fmt.Sprintf("uid-%d", next)
		mu.Unlock()
		role := "Regular"
		if body.Role != nil {
			role = string(*body.Role)
		}
		u := wekaapi.User{
			Uid:      &uid,
			Username: body.Username,
			Role:     str(role),
			PosixUid: body.PosixUid,
			PosixGid: body.PosixGid,
		}
		store.Store(uid, storedUser{user: u, password: deref(body.Password)})
		writeData(w, u)
	})
	f.Handle("GET /users", func(w http.ResponseWriter, r *http.Request) {
		calls.add(r)
		var all []wekaapi.User
		store.Range(func(_, v any) bool {
			all = append(all, v.(storedUser).user)
			return true
		})
		writeData(w, all)
	})
	f.Handle("PUT /users/{uid}", func(w http.ResponseWriter, r *http.Request) {
		calls.add(r)
		v, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		su := v.(storedUser)
		var body wekaapi.UpdateUserJSONBody
		decodeBody(t, r, &body)
		if body.Role != nil {
			role := string(*body.Role)
			su.user.Role = &role
		}
		if body.PosixUid != nil {
			su.user.PosixUid = body.PosixUid
		}
		if body.PosixGid != nil {
			su.user.PosixGid = body.PosixGid
		}
		store.Store(*su.user.Uid, su)
		writeData(w, su.user)
	})
	f.Handle("PUT /users/{uid}/password", func(w http.ResponseWriter, r *http.Request) {
		calls.add(r)
		v, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		su := v.(storedUser)
		var body wekaapi.SetUserPasswordJSONBody
		decodeBody(t, r, &body)
		su.password = body.Password
		store.Store(*su.user.Uid, su)
		writeData(w, "ok")
	})
	f.Handle("DELETE /users/{uid}", func(w http.ResponseWriter, r *http.Request) {
		calls.add(r)
		if _, ok := store.LoadAndDelete(r.PathValue("uid")); !ok {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeData(w, "ok")
	})
	return &store, &calls
}

func storedPassword(store *sync.Map, uid, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		v, ok := store.Load(uid)
		if !ok {
			return fmt.Errorf("user %s not stored", uid)
		}
		if got := v.(storedUser).password; got != want {
			return fmt.Errorf("stored password %q, want %q", got, want)
		}
		return nil
	}
}

func callsExact(calls *userCalls, from *int, want string, forbid ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got := calls.after(*from)
		saw := false
		for _, p := range got {
			for _, bad := range forbid {
				if p == bad {
					return fmt.Errorf("unexpected call %s in %v", p, got)
				}
			}
			if p == want {
				saw = true
			}
		}
		if want != "" && !saw {
			return fmt.Errorf("missing call %s in %v", want, got)
		}
		return nil
	}
}

func TestUserResource(t *testing.T) {
	f := newFakeWeka(t)
	store, calls := fakeUsers(t, f)
	var mark int

	const (
		user        = "weka_user.test"
		putUser     = "PUT /api/v2/users/uid-1"
		putPassword = "PUT /api/v2/users/uid-1/password"
	)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			left := 0
			store.Range(func(_, _ any) bool { left++; return true })
			if left != 0 {
				return fmt.Errorf("%d users left after destroy", left)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.config(`
resource "weka_user" "test" {
  username = "tfuser"
  password = "Secret-pass-1"
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(user, "id", "uid-1"),
					resource.TestCheckResourceAttr(user, "username", "tfuser"),
					resource.TestCheckResourceAttr(user, "password", "Secret-pass-1"),
					resource.TestCheckResourceAttr(user, "role", "Regular"),
					storedPassword(store, "uid-1", "Secret-pass-1"),
				),
			},
			{
				// GET omits password. Refresh must not plan a change.
				RefreshState: true,
			},
			{
				PreConfig: func() { mark = calls.n() },
				Config: f.config(`
resource "weka_user" "test" {
  username  = "tfuser"
  password  = "Secret-pass-1"
  role      = "ReadOnly"
  posix_uid = 10
  posix_gid = 20
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(user, "id", "uid-1"),
					resource.TestCheckResourceAttr(user, "role", "ReadOnly"),
					resource.TestCheckResourceAttr(user, "posix_uid", "10"),
					resource.TestCheckResourceAttr(user, "posix_gid", "20"),
					resource.TestCheckResourceAttr(user, "password", "Secret-pass-1"),
					storedPassword(store, "uid-1", "Secret-pass-1"),
					callsExact(calls, &mark, putUser, putPassword),
				),
			},
			{
				PreConfig: func() { mark = calls.n() },
				Config: f.config(`
resource "weka_user" "test" {
  username  = "tfuser"
  password  = "Secret-pass-2"
  role      = "ReadOnly"
  posix_uid = 10
  posix_gid = 20
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(user, "id", "uid-1"),
					resource.TestCheckResourceAttr(user, "password", "Secret-pass-2"),
					resource.TestCheckResourceAttr(user, "role", "ReadOnly"),
					storedPassword(store, "uid-1", "Secret-pass-2"),
					callsExact(calls, &mark, putPassword, putUser),
				),
			},
			{
				RefreshState: true,
			},
			{
				ResourceName:            user,
				ImportState:             true,
				ImportStateId:           "tfuser",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password"},
			},
			{
				ResourceName:            user,
				ImportState:             true,
				ImportStateId:           "uid-1",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password"},
			},
			{
				// Deleted outside Terraform: Read drops it and the plan recreates it.
				PreConfig: func() { store.Delete("uid-1") },
				Config: f.config(`
resource "weka_user" "test" {
  username  = "tfuser"
  password  = "Secret-pass-2"
  role      = "ReadOnly"
  posix_uid = 10
  posix_gid = 20
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(user, "id", "uid-2"),
					resource.TestCheckResourceAttr(user, "username", "tfuser"),
					resource.TestCheckResourceAttr(user, "password", "Secret-pass-2"),
					resource.TestCheckResourceAttr(user, "role", "ReadOnly"),
					storedPassword(store, "uid-2", "Secret-pass-2"),
				),
			},
		},
	})
}
