package api

import (
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/obs"
)

func TestBuildReleasePackageArtifactsUsesDistributableMTime(t *testing.T) {
	items := []obs.BinaryArtifact{
		{
			Project:  "isv:percona:ppg:releases:17",
			Repo:     "openSUSE_Tumbleweed",
			Arch:     "x86_64",
			Package:  "etcd",
			Filename: "etcd-3.5.30-2.1.x86_64.rpm",
			Size:     10,
			MTime:    1779201973,
			BuiltAt:  time.Unix(1779201973, 0).UTC(),
		},
		{
			Project:  "isv:percona:ppg:releases:17",
			Repo:     "openSUSE_Tumbleweed",
			Arch:     "x86_64",
			Package:  "etcd",
			Filename: "etcd-debugsource-3.5.30-2.1.x86_64.rpm",
			Size:     20,
			MTime:    1779202000,
			BuiltAt:  time.Unix(1779202000, 0).UTC(),
		},
	}

	artifacts := buildReleasePackageArtifacts(items, nil) // nil versions → empty Version
	if len(artifacts) != 1 {
		t.Fatalf("expected one package artifact, got %d", len(artifacts))
	}
	if len(artifacts[0].Binaries) != 1 {
		t.Fatalf("expected one distributable binary, got %d", len(artifacts[0].Binaries))
	}
	if artifacts[0].BuiltAt != "2026-05-19T14:46:13Z" {
		t.Fatalf("BuiltAt = %q", artifacts[0].BuiltAt)
	}
	if artifacts[0].Binaries[0].MTime != 1779201973 {
		t.Fatalf("binary MTime = %d", artifacts[0].Binaries[0].MTime)
	}
}

func TestBuildReleasePackageArtifactsVersion(t *testing.T) {
	items := []obs.BinaryArtifact{
		{
			Project:  "isv:percona:ppg:releases:17",
			Repo:     "openSUSE_Tumbleweed",
			Arch:     "x86_64",
			Package:  "etcd",
			Filename: "etcd-3.5.30-2.1.x86_64.rpm",
			Size:     10,
			MTime:    1779201973,
			BuiltAt:  time.Unix(1779201973, 0).UTC(),
		},
		{
			Project:  "isv:percona:ppg:releases:17",
			Repo:     "Ubuntu_24.04",
			Arch:     "x86_64",
			Package:  "etcd",
			Filename: "etcd_3.5.30-2ubuntu1_amd64.deb",
			Size:     10,
			MTime:    1779201973,
			BuiltAt:  time.Unix(1779201973, 0).UTC(),
		},
	}

	versions := map[string]string{
		"openSUSE_Tumbleweed\x00x86_64\x00etcd.rpm": "3.5.30-2.1",
		// Ubuntu_24.04 intentionally absent — Version should stay ""
	}

	artifacts := buildReleasePackageArtifacts(items, versions)

	var openSUSE, ubuntu *ReleasePackageArtifact
	for i := range artifacts {
		switch artifacts[i].Repo {
		case "openSUSE_Tumbleweed":
			openSUSE = &artifacts[i]
		case "Ubuntu_24.04":
			ubuntu = &artifacts[i]
		}
	}

	if openSUSE == nil {
		t.Fatal("openSUSE artifact missing")
	}
	if openSUSE.Version != "3.5.30-2.1" {
		t.Errorf("openSUSE Version = %q, want '3.5.30-2.1'", openSUSE.Version)
	}
	if ubuntu == nil {
		t.Fatal("Ubuntu artifact missing")
	}
	if ubuntu.Version != "" {
		t.Errorf("Ubuntu Version = %q, want ''", ubuntu.Version)
	}
}

func TestBinaryBaseName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"postgresql16-16.4-2.3.x86_64.rpm", "postgresql16.rpm"},
		{"postgresql16-devel-16.4-2.3.x86_64.rpm", "postgresql16-devel.rpm"},
		{"perl-YAML-LibYAML-0.88-1.1.noarch.rpm", "perl-YAML-LibYAML.rpm"},
		{"etcd-3.5.30-2.1.x86_64.rpm", "etcd.rpm"},
		{"etcd_3.5.30-2ubuntu1_amd64.deb", "etcd.deb"},
		{"postgresql-16_16.4-2ubuntu1_amd64.deb", "postgresql-16.deb"},
		{"something.containerinfo", "something.containerinfo"},
	}
	for _, c := range cases {
		got := binaryBaseName(c.in)
		if got != c.want {
			t.Errorf("binaryBaseName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
