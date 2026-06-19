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

	artifacts := buildReleasePackageArtifacts(items)
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
