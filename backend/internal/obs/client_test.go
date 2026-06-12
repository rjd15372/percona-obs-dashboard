package obs

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBasicAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<directory></directory>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "user", "pass")
	_, err := c.ListSubprojects(context.Background(), "isv:percona")
	if err != nil {
		t.Fatal(err)
	}

	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if gotAuth != expected {
		t.Errorf("auth header: got %q, want %q", gotAuth, expected)
	}
}

func TestListSubprojects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/source/isv:percona") {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`<directory>
			<entry name="ppg"/>
			<entry name="pmm"/>
		</directory>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	projects, err := c.ListSubprojects(context.Background(), "isv:percona")
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2, got %d: %v", len(projects), projects)
	}
	if projects[0] != "isv:percona:ppg" || projects[1] != "isv:percona:pmm" {
		t.Errorf("unexpected projects: %v", projects)
	}
}

func TestNon200Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", 401)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	_, err := c.ListSubprojects(context.Background(), "isv:percona")
	if err == nil {
		t.Fatal("expected error for 401")
	}
}
